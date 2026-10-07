package middleware

import (
	"net/http"
	"slices"
	"time"
)

// WriteDeadline gives a request to one of paths d to write its answer instead of the server's
// WriteTimeout, which stays for every other route. A cold analytics dataset takes longer to
// build than the server's timeout, which would cut the connection with nothing sent (HT-516).
// It must wrap the server's own ResponseWriter, or one that unwraps to it; when the writer
// cannot take a deadline, the server's timeout stays.
func WriteDeadline(d time.Duration, paths ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slices.Contains(paths, r.URL.Path) {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
			}
			next.ServeHTTP(w, r)
		})
	}
}
