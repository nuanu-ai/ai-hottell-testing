// Package mcp serves the MCP server of the service on /mcp over streamable HTTP, for the
// agents of the user and the hottell binary (docs/specs/hottell-contract/mcp.md).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// serverName is the name the server gives itself in the answer to initialize and the name
// of the server in the configs of the agents.
const serverName = "hottell"

// keepAlive is how often the server pings an MCP session subscribed to the settings so that
// its idle standalone GET stream is not closed by a proxy (mcp.md, section «Ресурс
// настроек»). Other sessions are not pinged: a ping reaches a client only over that stream,
// which a client that does not subscribe may never open.
const keepAlive = 30 * time.Second

// New returns the handler of /mcp: every request must carry an active MCP key in
// Authorization: Bearer, and the tools and the settings resource act for the user of that
// key. origin is HT_PUBLIC_ORIGIN, the base address of the OTLP ingest the collector token
// is sent to; version is the service version the server reports; logger records the
// failures of the key, user and settings stores.
func New(keys Keys, users Users, settings Settings, origin, version string, logger *slog.Logger) http.Handler {
	return newHandler(keys, users, settings, origin, version, logger, keepAlive)
}

// newHandler is New with the interval the server pings each subscribed session at.
func newHandler(
	keys Keys, users Users, settings Settings, origin, version string, logger *slog.Logger, keepAlive time.Duration,
) http.Handler {
	sessions := newOwners()
	// Each session gets a server of its own, bound to the user that opened it: the
	// server's ResourceUpdated then reaches that session alone. A request of an open
	// session consults the server only for the protocol versions, which every server
	// shares.
	versions := sdkmcp.NewServer(&sdkmcp.Implementation{Name: serverName, Version: version}, nil)
	getServer := func(r *http.Request) *sdkmcp.Server {
		if r.Header.Get(sessionHeader) != "" {
			return versions
		}
		userID, ok := r.Context().Value(userKey{}).(uuid.UUID)
		if !ok {
			return nil
		}
		return newSessionServer(sessions, keys, users, settings, userID, origin, version, logger, keepAlive)
	}
	handler := sdkmcp.NewStreamableHTTPHandler(getServer, &sdkmcp.StreamableHTTPOptions{Logger: logger})
	// RequireBearerToken hands the user to the SDK, which binds each MCP session to the user
	// that opened it and passes the user to the tools in the request's TokenInfo.
	bound := auth.RequireBearerToken(resolvedUser, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})
	return authenticate(keys, logger)(sessions.guard(bound(unbounded(handler))))
}

// unbounded lifts the write deadline of the HTTP server from the standalone GET stream of
// a session, which lives as long as the session: under the deadline the server would cut
// the stream and the subscribed client would have to reopen it.
func unbounded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}

// newSessionServer returns the server of a new MCP session of the user with userID.
func newSessionServer(
	sessions *owners, keys Keys, users Users, settings Settings, userID uuid.UUID, origin, version string,
	logger *slog.Logger, keepAlive time.Duration,
) *sdkmcp.Server {
	sub := &subscription{settings: settings, userID: userID, keepAlive: keepAlive, logger: logger}
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: serverName, Version: version}, &sdkmcp.ServerOptions{
		Logger: logger,
		Capabilities: &sdkmcp.ServerCapabilities{
			Tools:     &sdkmcp.ToolCapabilities{},
			Resources: &sdkmcp.ResourceCapabilities{Subscribe: true},
		},
		SubscribeHandler:   sub.subscribe,
		UnsubscribeHandler: sub.unsubscribe,
	})
	sub.server = server
	server.AddReceivingMiddleware(sessions.record)
	addPing(server, users, version, logger)
	addCollectorToken(server, keys, origin, logger)
	addInstall(server, origin)
	addSettings(server, settings, userID, logger)
	return server
}

// userKey is the context key of the id of the user whose MCP key a request carries.
type userKey struct{}

// authenticate answers 401 when a request carries no active MCP key and 503 when the key
// cannot be checked; otherwise it puts the id of the key's user into the request context.
func authenticate(keys Keys, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			key, ok := bearer(header)
			if !ok {
				writeUnauthorized(w, header != "")
				return
			}
			userID, err := keys.Resolve(r.Context(), domain.AccessKeyKindMCP, key)
			switch {
			case errors.Is(err, domain.ErrAccessKeyNotFound), errors.Is(err, domain.ErrAccessKeyRevoked):
				writeUnauthorized(w, true)
				return
			case err != nil:
				logger.ErrorContext(r.Context(), "resolve MCP key", "error", err)
				writeError(w, http.StatusServiceUnavailable, "unavailable", "MCP key cannot be checked, try again later")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, userID)))
		})
	}
}

// resolvedUser is the TokenVerifier of a request authenticate has let through: the key is
// already checked, so it only reports the user authenticate found.
func resolvedUser(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
	userID, ok := ctx.Value(userKey{}).(uuid.UUID)
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: userID.String()}, nil
}

// bearer returns the key of an Authorization header of the Bearer scheme, the scheme
// matched without regard to case and followed by exactly one space.
func bearer(header string) (string, bool) {
	const scheme = "bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	key := header[len(scheme):]
	if strings.ContainsAny(key, " \t") {
		return "", false
	}
	return key, true
}

// writeUnauthorized answers 401 the same way whatever was wrong with the key; the challenge
// carries error="invalid_token" only when the request had an Authorization header
// (RFC 6750, section 3.1).
func writeUnauthorized(w http.ResponseWriter, withHeader bool) {
	challenge := `Bearer realm="hottell"`
	if withHeader {
		challenge += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeError(w, http.StatusUnauthorized, "unauthorized",
		"MCP key is missing, unknown or revoked; issue a new one on the Connect page")
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: code, Message: message})
}
