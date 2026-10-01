// Package ioc assembles the service runtime.
package ioc

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/argon2id"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/clock"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/gitea"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/pubsub"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/token"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/webauthn"
	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/application/keys"
	"git.alva.dev/alva/harness-telemetry/internal/application/passkeys"
	"git.alva.dev/alva/harness-telemetry/internal/application/session"
	"git.alva.dev/alva/harness-telemetry/internal/application/settings"
	"git.alva.dev/alva/harness-telemetry/internal/application/users"
	"git.alva.dev/alva/harness-telemetry/internal/config"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/ingest"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/spa"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
)

// apiBaseURL is the prefix every operation of the OpenAPI contract is served under.
const apiBaseURL = "/api"

// mcpPath is the path the MCP server is served at, under the public origin.
const mcpPath = "/mcp"

// openOperations are the operationIds answered without a session (HT-3, section API);
// every other operation needs one.
func openOperations() []string {
	return []string{
		"login",
		"beginPasskeyLogin",
		"finishPasskeyLogin",
		"getInvite",
		"acceptInvite",
		"getPasswordReset",
		"completePasswordReset",
		"getVersion",
	}
}

// Stores are the repositories the use cases keep their state in.
type Stores struct {
	Users users.Users
	// PasskeyUsers are the same users as the passkey use cases reach them.
	PasskeyUsers passkeys.Users
	Links        users.Links
	Sessions     session.Sessions
	Passkeys     passkeys.Passkeys
	Ceremonies   passkeys.Ceremonies
	AccessKeys   keys.AccessKeys
	Settings     settings.Settings
	Tx           auth.TxManager
}

// PostgresStores returns the Stores kept in the PostgreSQL database of pool.
func PostgresStores(pool *pgxpool.Pool) Stores {
	usersRepo := postgres.NewUsers(pool)
	return Stores{
		Users:        usersRepo,
		PasskeyUsers: usersRepo,
		Links:        postgres.NewLinks(pool),
		Sessions:     postgres.NewSessions(pool),
		Passkeys:     postgres.NewPasskeys(pool),
		Ceremonies:   postgres.NewCeremonies(pool),
		AccessKeys:   postgres.NewAccessKeys(pool),
		Settings:     postgres.NewTelemetrySettings(pool),
		Tx:           postgres.NewTxManager(pool),
	}
}

// Container holds the assembled runtime.
type Container struct {
	Handler http.Handler
	Health  *handler.Health
}

// New assembles the HTTP handler: the health probes, the API under /api, the OTLP ingest on
// /v1/logs, /v1/metrics and /v1/traces, the MCP server on /mcp, the hottell release files on
// /install.sh and /download/ and the SPA on every other path, all behind the security headers.
// db is the database the readiness probe pings; stores keep the users, their links and
// sessions, passkeys, WebAuthn ceremonies, access keys and deny settings; cfg sets the
// allowed origins, the cookies, the origin links and the MCP address are built from, the
// WebAuthn relying party, the collector the ingest passes requests to and the Gitea the
// hottell releases are taken from; version is the build version the API reports; logger
// records the operations that fail; app is the root of the built frontend. It fails when
// cfg holds no valid WebAuthn relying party.
func New(
	db handler.Pinger, stores Stores, cfg config.Config, version string, logger *slog.Logger, app fs.FS,
) (Container, error) {
	relyingParty, err := webauthn.New(cfg.WebAuthnRPID, cfg.WebAuthnRPOrigins)
	if err != nil {
		return Container{}, fmt.Errorf("webauthn relying party: %w", err)
	}
	health := handler.NewHealth(db)
	systemClock := clock.System{}
	sessionService := session.NewService(stores.Sessions, token.Issuer{}, systemClock)
	hasher := argon2id.New(argon2id.DefaultMemoryKiB)
	authService := auth.NewService(stores.Users, hasher, sessionService, systemClock, stores.Tx)
	usersService := users.NewService(stores.Users, stores.Links, stores.Sessions, sessionService, hasher, token.Issuer{},
		systemClock, stores.Tx, users.PublicOrigin(cfg.PublicOrigin))
	passkeysService := passkeys.NewService(stores.PasskeyUsers, stores.Passkeys, stores.Ceremonies, relyingParty,
		sessionService, systemClock)
	keysService := keys.NewService(stores.AccessKeys, token.Issuer{}, systemClock)
	settingsService := settings.NewService(stores.Settings, pubsub.NewNotifier())
	cookies := middleware.NewCookies(cfg.CookieSecure, systemClock)
	responseError := handler.ResponseError(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)

	api := openapi.NewStrictHandlerWithOptions(
		handler.NewAPI(version, authService, sessionService, usersService, passkeysService, keysService,
			settingsService, cookies, cfg.PublicOrigin+mcpPath),
		[]openapi.StrictMiddlewareFunc{
			middleware.RequireSession(openOperations()...),
			middleware.EndCeremony(cookies, "finishPasskeyLogin", "finishPasskeyRegistration"),
		},
		openapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  handler.RequestError,
			ResponseErrorHandlerFunc: responseError,
		},
	)
	openapi.HandlerWithOptions(api, openapi.StdHTTPServerOptions{
		BaseURL:          apiBaseURL,
		BaseRouter:       mux,
		Middlewares:      []openapi.MiddlewareFunc{middleware.Session(sessionService, cookies, responseError)},
		ErrorHandlerFunc: handler.RequestError,
	})
	mux.HandleFunc(apiBaseURL+"/", handler.NotFound)
	// The ingest authenticates by the collector token alone: no session, and the Origin
	// check guards only /api.
	ingestHandler := ingest.New(keysService, cfg.CollectorURL, logger)
	for _, path := range ingest.Paths() {
		mux.Handle(path, ingestHandler)
	}
	// The MCP server authenticates by the MCP key alone, like the ingest.
	mux.Handle(mcpPath, mcp.New(keysService, authService, settingsService, cfg.PublicOrigin, version, logger))
	// The hottell release files are public, like the install command that fetches them.
	download := handler.NewDownload(gitea.New(cfg.GiteaURL, cfg.GiteaRepo, cfg.GiteaToken, systemClock.Now),
		cfg.PublicOrigin, logger)
	mux.HandleFunc("GET /install.sh", download.InstallScript)
	// {name...} takes nested paths too, so they answer 404 here instead of reaching the SPA.
	mux.HandleFunc("GET /download/{name...}", download.File)
	// Not "GET /": it would conflict with "/api/", which answers every method.
	mux.Handle("/", spa.New(app))

	allowedOrigins := append([]string{cfg.PublicOrigin}, cfg.WebAuthnRPOrigins...)
	return Container{Handler: handler.SecurityHeaders(middleware.Origin(allowedOrigins)(mux)), Health: health}, nil
}
