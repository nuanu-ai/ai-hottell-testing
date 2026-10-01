package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Error codes of the common Error body.
const (
	codeNotFound   = "not_found"
	codeBadRequest = "bad_request"
	codeInternal   = "internal"

	messageInternal = "Что-то пошло не так. Попробуйте ещё раз"
)

// NotFound answers a path under /api that no operation of the contract matches.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, codeNotFound, "no such API route")
}

// RequestError answers a request the contract rejects before it reaches an operation.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
}

// ResponseError returns the handler that answers an operation that failed or produced
// no valid response. A domain error is answered with its status, code and message; any
// other error is logged and answered 500 internal, its cause not exposed to the client.
// The log names the route pattern, not the path, so a token in the path stays out of it.
func ResponseError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var public *domain.Error
		if errors.As(err, &public) {
			if status, ok := statusOf(public); ok {
				writeError(w, status, public.Code, public.Message)
				return
			}
		}
		logger.ErrorContext(r.Context(), "api operation failed", "method", r.Method, "route", r.Pattern, "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, messageInternal)
	}
}

// statusOf returns the HTTP status a domain error is answered with; false for a domain
// error that has none, which is then answered as an internal one.
func statusOf(err *domain.Error) (int, bool) {
	switch err {
	case domain.ErrInvalidEmail, domain.ErrInvalidName, domain.ErrInvalidPasskeyName,
		domain.ErrPasswordTooShort, domain.ErrPasswordTooLong, domain.ErrWrongCurrentPassword,
		domain.ErrPasskeyCeremonyExpired, domain.ErrPasskeyVerificationFailed:
		return http.StatusBadRequest, true
	case domain.ErrInvalidCredentials, domain.ErrUnauthenticated:
		return http.StatusUnauthorized, true
	case domain.ErrLinkNotFound, domain.ErrUserNotFound, domain.ErrPasskeyNotFound, domain.ErrAccessKeyNotFound:
		return http.StatusNotFound, true
	case domain.ErrEmailTaken, domain.ErrUserNotInvited, domain.ErrUserNotActive, domain.ErrCannotResetSelf,
		domain.ErrSettingsVersionConflict:
		return http.StatusConflict, true
	case domain.ErrLinkUsed, domain.ErrLinkExpired:
		return http.StatusGone, true
	case domain.ErrInvalidTelemetrySettings:
		return http.StatusUnprocessableEntity, true
	default:
		return 0, false
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openapi.Error{Code: code, Message: message})
}
