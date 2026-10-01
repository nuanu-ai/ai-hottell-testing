package mcp

import (
	"context"
	"net/http"
	"sync"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessionHeader is the header a client names its MCP session in.
const sessionHeader = "Mcp-Session-Id"

// owners records the user each open MCP session belongs to, so that a session named with
// the key of another user is answered 404 like an unknown one (mcp.md, section «Сервер»).
// The SDK refuses such a request by itself too, but with 403.
type owners struct {
	mu    sync.Mutex
	users map[string]uuid.UUID
}

func newOwners() *owners {
	return &owners{users: make(map[string]uuid.UUID)}
}

// record is a receiving middleware of the server: on initialize it records the user of the
// session being opened and forgets it once the session closes.
func (o *owners) record(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		session, ok := req.GetSession().(*sdkmcp.ServerSession)
		extra := req.GetExtra()
		if method != "initialize" || !ok || extra == nil || extra.TokenInfo == nil {
			return next(ctx, method, req)
		}
		if userID, err := uuid.Parse(extra.TokenInfo.UserID); err == nil {
			id := session.ID()
			o.mu.Lock()
			o.users[id] = userID
			o.mu.Unlock()
			go func() {
				_ = session.Wait()
				o.mu.Lock()
				delete(o.users, id)
				o.mu.Unlock()
			}()
		}
		return next(ctx, method, req)
	}
}

// guard answers 404 to a request that names a session of another user than the one
// authenticate put into its context.
func (o *owners) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(sessionHeader); id != "" {
			o.mu.Lock()
			owner, known := o.users[id]
			o.mu.Unlock()
			if userID, _ := r.Context().Value(userKey{}).(uuid.UUID); known && owner != userID {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
