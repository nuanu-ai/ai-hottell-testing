// Command api runs the harness-telemetry HTTP service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// The zones of the browsers the dataset lays its days out in (tz, HT-514), whatever the
	// image carries.
	_ "time/tzdata"

	"git.alva.dev/alva/harness-telemetry/cmd/api/ioc"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/config"
	"git.alva.dev/alva/harness-telemetry/web"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	// sessionIndexRetry is how often the check of the session index tries again while
	// ClickHouse cannot answer.
	sessionIndexRetry = time.Minute
)

// version is the build version the API reports, set with -ldflags "-X main.version=...".
var version = "dev" //nolint:gochecknoglobals // the linker can only set a package-level variable

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		logger.Error("load config", "error", err)
		return 1
	}
	logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		return 1
	}
	defer pool.Close()

	sessions := postgres.NewSessions(pool)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		cleanExpired(ctx, map[string]expiredRecords{
			"sessions":            sessions,
			"webauthn_ceremonies": postgres.NewCeremonies(pool),
		}, cleanupInterval, logger)
	}()
	// Runs before pool.Close: the cleanup stops with the context and releases the pool first.
	defer func() {
		stop()
		<-cleanupDone
	}()

	app, err := web.App()
	if err != nil {
		logger.Error("open frontend", "error", err)
		return 1
	}
	container, err := ioc.New(pool, ioc.PostgresStores(pool), cfg, version, logger, app)
	if err != nil {
		logger.Error("assemble service", "error", err)
		return 1
	}
	// Runs after server.Shutdown, which the return paths below reach first, and before
	// pool.Close, deferred earlier.
	defer func() {
		if err := container.Close(); err != nil {
			logger.Error("close service", "error", err)
		}
	}()
	indexDone := make(chan struct{})
	go func() {
		defer close(indexDone)
		container.CheckSessionIndex(ctx, logger, sessionIndexRetry)
	}()
	// Runs before container.Close: the check stops with the context and leaves the reader first.
	defer func() {
		stop()
		<-indexDone
	}()
	warmDone := make(chan struct{})
	go func() {
		defer close(warmDone)
		container.WarmAnalytics(ctx, logger)
	}()
	// Runs before container.Close: the warm-up stops with the context, cancelling its build, and
	// leaves the reader first.
	defer func() {
		stop()
		<-warmDone
	}()
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           container.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		logger.Error("listen", "addr", cfg.HTTPAddr, "error", err)
		return 1
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	container.Health.MarkReady()
	logger.Info("server started", "addr", listener.Addr().String())

	select {
	case err := <-serveErr:
		logger.Error("serve", "error", err)
		return 1
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "error", err)
		return 1
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		return 1
	}
	logger.Info("server stopped")
	return 0
}
