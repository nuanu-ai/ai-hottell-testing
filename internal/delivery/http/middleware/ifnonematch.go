package middleware

import (
	"net/http"
	"strings"
)

// JoinIfNoneMatch joins repeated If-None-Match lines of a request into one comma-separated line,
// which is the same list (RFC 9110 §5.3): the generated binding of a header parameter takes one
// line and answers 400 to several (HT-489).
func JoinIfNoneMatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("If-None-Match"); len(values) > 1 {
			r = r.Clone(r.Context())
			r.Header.Set("If-None-Match", strings.Join(values, ", "))
		}
		next.ServeHTTP(w, r)
	})
}
