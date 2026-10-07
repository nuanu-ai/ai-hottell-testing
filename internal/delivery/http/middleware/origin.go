package middleware

import (
	"mime"
	"net/http"
	"strings"
)

const apiPrefix = "/api"

// Origin guards against CSRF: a POST, PUT, PATCH or DELETE under /api is accepted only
// with an Origin header from allowed, else 403 forbidden_origin; one with a body only
// with Content-Type application/json, else 415 unsupported_media_type.
func Origin(allowed []string) func(http.Handler) http.Handler {
	origins := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		origins[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAPI(r.URL.Path) || !isUnsafe(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := origins[r.Header.Get("Origin")]; !ok {
				writeError(w, http.StatusForbidden, codeForbiddenOrigin, messageForbiddenOrigin)
				return
			}
			// ContentLength is -1 when the length is unknown, e.g. a chunked body.
			if r.ContentLength != 0 && !isJSON(r.Header.Get("Content-Type")) {
				writeError(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType, messageUnsupportedMedia)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isAPI(path string) bool {
	return path == apiPrefix || strings.HasPrefix(path, apiPrefix+"/")
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}
