package middleware

import (
	"context"
	"net/http"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

// EndCeremony deletes the ceremony cookie in the answer of each operation whose
// operationId is one of finish, whatever the operation answers: finishing uses the
// ceremony up either way. finish lists operationIds as the contract spells them.
func EndCeremony(cookies *Cookies, finish ...string) openapi.StrictMiddlewareFunc {
	finishOps := make(map[string]struct{}, len(finish))
	for _, op := range finish {
		finishOps[lowerFirst(op)] = struct{}{}
	}
	return func(next openapi.StrictHandlerFunc, operationID string) openapi.StrictHandlerFunc {
		if _, ok := finishOps[lowerFirst(operationID)]; !ok {
			return next
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			http.SetCookie(w, cookies.ExpiredCeremonyCookie())
			return next(ctx, w, r, request)
		}
	}
}
