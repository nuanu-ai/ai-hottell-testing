// Package ioc assembles the service runtime.
package ioc

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/argon2id"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/clickhouse"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/clock"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/filerelease"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/pubsub"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/token"
	"git.alva.dev/alva/harness-telemetry/internal/adapters/webauthn"
	"git.alva.dev/alva/harness-telemetry/internal/application/analytics"
	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
	"git.alva.dev/alva/harness-telemetry/internal/application/deep"
	"git.alva.dev/alva/harness-telemetry/internal/application/deliverystatus"
	"git.alva.dev/alva/harness-telemetry/internal/application/journal"
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
	jr "git.alva.dev/alva/harness-telemetry/internal/domain/journal"
)

// apiBaseURL is the prefix every operation of the OpenAPI contract is served under.
const apiBaseURL = "/api"

// mcpPath is the path the MCP server is served at, under the public origin.
const mcpPath = "/mcp"

// datasetWriteTimeout is how long the dataset and team routes may take to write their answer: a
// cold build takes longer than the server's WriteTimeout, which stays for every other route
// (HT-516, HT-531). The build itself is bounded by the analytics service.
const datasetWriteTimeout = 2 * time.Minute

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
	// DeliveryStatuses keep the delivery status the hottell binaries report.
	DeliveryStatuses deliverystatus.Statuses
	// AnalyticsKeys are the access keys as the delivery status of the analytics reads them.
	AnalyticsKeys analytics.Keys
	// HiddenTopics keep the analytics topics each user hid.
	HiddenTopics analytics.HiddenTopicsStore
	// Deep keeps the Deep reports, the registry, the decision journal and the skill reports;
	// a nil one leaves the retro, coach and registry tools of the MCP server out.
	Deep *DeepStores
	Tx   auth.TxManager
}

// DeepStores are the repositories of the Deep review, the registry and the decision journal.
type DeepStores struct {
	Reports interface {
		deep.Reports
		deepReportReader
	}
	Proposals interface {
		deep.Proposals
		proposalReader
	}
	Journal interface {
		deep.Journal
		journal.Journal
		// Verify checks the hash chain against want (nil: the links only): how many records
		// hold, or an error naming the first that does not (journalBreak reads its seq).
		Verify(ctx context.Context, want *jr.Head) (int, jr.Head, error)
	}
	SkillReports deep.SkillReports
	Tx           deep.TxManager
}

// PostgresStores returns the Stores kept in the PostgreSQL database of pool.
func PostgresStores(pool *pgxpool.Pool) Stores {
	usersRepo := postgres.NewUsers(pool)
	accessKeys := postgres.NewAccessKeys(pool)
	return Stores{
		Users:            usersRepo,
		PasskeyUsers:     usersRepo,
		Links:            postgres.NewLinks(pool),
		Sessions:         postgres.NewSessions(pool),
		Passkeys:         postgres.NewPasskeys(pool),
		Ceremonies:       postgres.NewCeremonies(pool),
		AccessKeys:       accessKeys,
		Settings:         postgres.NewTelemetrySettings(pool),
		DeliveryStatuses: postgres.NewDeliveryStatuses(pool),
		AnalyticsKeys:    accessKeys,
		HiddenTopics:     postgres.NewHiddenTopics(pool),
		Deep: &DeepStores{
			Reports: postgres.NewDeepReports(pool), Proposals: postgres.NewProposals(pool),
			Journal: postgres.NewDecisionJournal(pool), SkillReports: postgres.NewSkillOpportunities(pool),
			Tx: postgres.NewTxManager(pool),
		},
		Tx: postgres.NewTxManager(pool),
	}
}

// Container holds the assembled runtime. The caller closes it on shutdown.
type Container struct {
	Handler http.Handler
	Health  *handler.Health
	// telemetry reads the telemetry from ClickHouse. It connects on the first read, so the
	// service starts and serves the ingest and sign-in while ClickHouse is down.
	telemetry *clickhouse.Reader
	// analytics is the analytics service the warm-up keeps the datasets of.
	analytics *analytics.Service
}

// WarmAnalytics keeps the datasets of the header's periods built: at once and every
// analytics.WarmEvery, one build at a time, until ctx ends (HT-538). A failed build is logged and
// tried again on the next tick; ClickHouse being down fails nothing else.
func (c Container) WarmAnalytics(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(analytics.WarmEvery)
	defer ticker.Stop()
	c.analytics.Warm(ctx, logger, ticker.C)
}

// CheckSessionIndex logs a warning when otel.otel_logs lacks clickhouse.SessionIndex, the bloom
// filter the collector creates and the reads of one session rely on: without it each of them
// scans the service's history (HT-388). While ClickHouse cannot answer it tries again every
// retry; it returns at the first answer or when ctx ends.
func (c Container) CheckSessionIndex(ctx context.Context, logger *slog.Logger, retry time.Duration) {
	for {
		ok, err := c.telemetry.HasSessionIndex(ctx)
		if err == nil {
			if !ok {
				logger.WarnContext(ctx, "otel.otel_logs has no session index: reads of one session scan the whole service",
					"index", clickhouse.SessionIndex)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

// Close closes the connections the container opened.
func (c Container) Close() error {
	if err := c.telemetry.Close(); err != nil {
		return fmt.Errorf("close telemetry reader: %w", err)
	}
	return nil
}

// New assembles the HTTP handler: the health probes, the API under /api, the OTLP ingest on
// /v1/logs, /v1/metrics and /v1/traces, the MCP server on /mcp, the hottell release files on
// /install.sh and /download/ and the SPA on every other path, all behind the security headers.
// db is the database the readiness probe pings; stores keep the users, their links and
// sessions, passkeys, WebAuthn ceremonies, access keys, deny settings and delivery statuses; cfg sets the
// allowed origins, the cookies, the origin links and the MCP address are built from, the
// WebAuthn relying party, the collector the ingest passes requests to, the directory the
// client release files are served from and the ClickHouse the telemetry is read from; version is
// the build version the API reports; logger records the operations that fail; app is the root
// of the built frontend. It fails when cfg holds no valid WebAuthn relying party or a
// ClickHouse URL that does not parse; ClickHouse being down does not fail it.
func New(
	db handler.Pinger, stores Stores, cfg config.Config, version string, logger *slog.Logger, app fs.FS,
) (Container, error) {
	relyingParty, err := webauthn.New(cfg.WebAuthnRPID, cfg.WebAuthnRPOrigins)
	if err != nil {
		return Container{}, fmt.Errorf("webauthn relying party: %w", err)
	}
	// Not pinged here: the readiness probe and the start of the service never wait for
	// ClickHouse, only the reads do.
	telemetryReader, err := clickhouse.Open(cfg.ClickHouseURL, clickhouse.WithLogger(logger),
		clickhouse.WithAggregates(os.Getenv("HT_CLICKHOUSE_AGGREGATES") == "1"))
	if err != nil {
		return Container{}, fmt.Errorf("telemetry reader: %w", err)
	}
	// The aggregate tables of the period reads (HT-536), created and filled over the admin user.
	if adminURL := adminClickHouseURL(); adminURL != "" {
		go clickhouse.EnsureAggregates(context.Background(), adminURL, logger)
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
	deliveryStatusService := deliverystatus.NewService(stores.DeliveryStatuses, systemClock)
	// The analytics reads ClickHouse on request: while it is down its operations answer 503.
	analyticsService := analytics.NewService(telemetryReader, userNames{users: stores.Users}, systemClock,
		analytics.WithDelivery(analytics.DeliveryPorts{
			Keys: stores.AnalyticsKeys, Received: telemetryReader, Settings: stores.Settings, Reports: stores.DeliveryStatuses,
		}),
		analytics.WithHiddenTopics(stores.HiddenTopics))
	cookies := middleware.NewCookies(cfg.CookieSecure, systemClock)
	responseError := handler.ResponseError(logger)
	review := wireDeep(stores.Deep, telemetryReader, systemClock, stores.Users)
	if review.tools != nil {
		review.tools = append(review.tools, mcp.WithLiveFindings(liveFindings{analytics: analyticsService, now: systemClock.Now}))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)

	api := openapi.NewStrictHandlerWithOptions(
		handler.NewAPI(version, authService, sessionService, usersService, passkeysService, keysService,
			settingsService, cookies, cfg.PublicOrigin+mcpPath).WithAnalytics(analyticsService).WithReview(review.review),
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
	mux.Handle(mcpPath, mcp.New(keysService, authService, settingsService, deliveryStatusService, cfg.PublicOrigin,
		version, logger, review.tools...))
	// The hottell release files are public, like the install command that fetches them.
	download := handler.NewDownload(filerelease.New(cfg.ReleaseDir),
		cfg.PublicOrigin, logger)
	mux.HandleFunc("GET /install.sh", download.InstallScript)
	// {name...} takes nested paths too, so they answer 404 here instead of reaching the SPA.
	mux.HandleFunc("GET /download/{name...}", download.File)
	// Not "GET /": it would conflict with "/api/", which answers every method.
	mux.Handle("/", spa.New(app))

	allowedOrigins := append([]string{cfg.PublicOrigin}, cfg.WebAuthnRPOrigins...)
	return Container{
		// Outermost: the write deadline is set on the server's own ResponseWriter; Gzip, innermost,
		// unwraps to it.
		Handler: middleware.WriteDeadline(datasetWriteTimeout, apiBaseURL+"/analytics/dataset", apiBaseURL+"/analytics/team")(
			handler.SecurityHeaders(middleware.Origin(allowedOrigins)(middleware.Gzip(middleware.JoinIfNoneMatch(mux))))),
		Health:    health,
		telemetry: telemetryReader,
		analytics: analyticsService,
	}, nil
}

// deepWiring is the Deep review and the decision journal as the MCP server and the API use them.
type deepWiring struct {
	tools  []mcp.Option
	review handler.Review
}

// wireDeep builds the Deep and journal services on stores: the retro, coach and registry
// tools of the MCP server and the review the API reads; nothing without the stores.
func wireDeep(stores *DeepStores, transcripts deep.Transcripts, clock journal.Clock, people users.Users) deepWiring {
	if stores == nil {
		return deepWiring{}
	}
	deepService := deep.NewService(deep.Ports{
		DeepReports: stores.Reports, SkillReports: stores.SkillReports, Journal: stores.Journal,
		Transcripts: transcripts, Proposals: stores.Proposals, Tx: stores.Tx,
	})
	journalService := journal.NewService(stores.Journal, deepService, stores.Tx, clock)
	tools := deepTools{deep: deepService, journal: journalService, reports: stores.Reports, proposals: stores.Proposals}
	return deepWiring{
		tools: []mcp.Option{
			mcp.WithDeep(tools), mcp.WithJournal(tools), mcp.WithRegistry(tools),
			mcp.WithSkillReports(skillTools{deep: deepService}),
		},
		review: reviewReads{stores: stores, deep: deepService, journal: journalService, people: userNames{users: people}},
	}
}

// userNames names the people of the analytics from the list of users.
type userNames struct {
	users users.Users
}

// Names returns the name of each of ids that is a user.
func (u userNames) Names(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	all, err := u.users.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	want := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	names := make(map[uuid.UUID]string, len(ids))
	for _, user := range all {
		if want[user.ID] {
			names[user.ID] = string(user.Name)
		}
	}
	return names, nil
}

// adminClickHouseURL is HT_CLICKHOUSE_ADMIN_URL with the password of HT_CLICKHOUSE_ADMIN_PASSWORD_FILE
// in its user info; empty without the URL or when either cannot be read.
func adminClickHouseURL() string {
	raw := os.Getenv("HT_CLICKHOUSE_ADMIN_URL")
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return ""
	}
	if path := os.Getenv("HT_CLICKHOUSE_ADMIN_PASSWORD_FILE"); path != "" {
		pw, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		u.User = url.UserPassword(u.User.Username(), strings.TrimSpace(string(pw)))
	}
	return u.String()
}
