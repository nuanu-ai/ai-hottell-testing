package middleware

import (
	"encoding/json"
	"net/http"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

// Errors the middleware answers itself, before a request reaches an operation.
const (
	codeForbiddenOrigin      = "forbidden_origin"
	messageForbiddenOrigin   = "Запрос пришёл не с этого сайта"
	codeUnsupportedMediaType = "unsupported_media_type"
	messageUnsupportedMedia  = "Тело запроса должно быть в формате JSON"
)

// ErrorHandler answers a request that failed with err.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openapi.Error{Code: code, Message: message})
}
