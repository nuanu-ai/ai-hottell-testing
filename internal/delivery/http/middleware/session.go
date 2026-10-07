package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// SessionResolver recognizes the session of a token, extending it with activity.
type SessionResolver interface {
	// Resolve returns the live session of token and its new expiry when it moved, nil
	// when it did not; domain.ErrUnauthenticated when the token has no live session.
	Resolve(ctx context.Context, token string) (domain.Session, *time.Time, error)
}

type sessionKey struct{}

type signedIn struct {
	userID    uuid.UUID
	sessionID uuid.UUID
}

// UserID returns the user of the session the request came with.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	s, ok := ctx.Value(sessionKey{}).(signedIn)
	return s.userID, ok
}

// SessionID returns the session the request came with.
func SessionID(ctx context.Context) (uuid.UUID, bool) {
	s, ok := ctx.Value(sessionKey{}).(signedIn)
	return s.sessionID, ok
}

// Session recognizes the session cookie of a request. A live session puts its user and
// session IDs in the request context and, when it was extended, sets the cookie again
// with the new Max-Age; a cookie with no live session is deleted and the request goes
// on without a session. Any other failure is answered by onError.
func Session(sessions SessionResolver, cookies *Cookies, onError ErrorHandler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			session, expiresAt, err := sessions.Resolve(r.Context(), cookie.Value)
			if errors.Is(err, domain.ErrUnauthenticated) {
				cookies.ClearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				onError(w, r, err)
				return
			}
			if expiresAt != nil {
				cookies.SetSessionCookie(w, cookie.Value, *expiresAt)
			}
			ctx := context.WithValue(r.Context(), sessionKey{}, signedIn{userID: session.UserID, sessionID: session.ID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireSession fails an operation that the request has no session for with
// domain.ErrUnauthenticated, unless its operationId is one of open. The generated
// server passes operation names with the first letter capitalized; open lists them as
// the contract spells them.
func RequireSession(open ...string) openapi.StrictMiddlewareFunc {
	openOps := make(map[string]struct{}, len(open))
	for _, op := range open {
		openOps[lowerFirst(op)] = struct{}{}
	}
	return func(next openapi.StrictHandlerFunc, operationID string) openapi.StrictHandlerFunc {
		if _, ok := openOps[lowerFirst(operationID)]; ok {
			return next
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if _, ok := UserID(ctx); !ok {
				return nil, domain.ErrUnauthenticated
			}
			return next(ctx, w, r, request)
		}
	}
}

// lowerFirst returns s with its first letter in lower case: getVersion for GetVersion.
func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}
