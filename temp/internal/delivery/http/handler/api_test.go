package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestAPIGetVersion(t *testing.T) {
	t.Parallel()

	srv := openapi.Handler(openapi.NewStrictHandler(handler.NewAPI("1.2.3", nil, nil, nil, nil, nil, nil, nil, ""), nil))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/version", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("got Content-Type %q, want application/json", ct)
	}
	var got openapi.Version
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Version != "1.2.3" {
		t.Fatalf("got version %q, want %q", got.Version, "1.2.3")
	}
}

func TestErrorResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		write    http.HandlerFunc
		wantCode int
		wantBody openapi.Error
	}{
		{
			name:     "not found",
			write:    handler.NotFound,
			wantCode: http.StatusNotFound,
			wantBody: openapi.Error{Code: "not_found", Message: "no such API route"},
		},
		{
			name: "request error",
			write: func(w http.ResponseWriter, r *http.Request) {
				handler.RequestError(w, r, errors.New("bad parameter"))
			},
			wantCode: http.StatusBadRequest,
			wantBody: openapi.Error{Code: "bad_request", Message: "bad parameter"},
		},
		{
			name: "response error hides the cause",
			write: func(w http.ResponseWriter, r *http.Request) {
				handler.ResponseError(slog.New(slog.DiscardHandler))(w, r, errors.New("secret detail"))
			},
			wantCode: http.StatusInternalServerError,
			wantBody: openapi.Error{Code: "internal", Message: "Что-то пошло не так. Попробуйте ещё раз"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			tt.write(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

			if rec.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantCode)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("got Content-Type %q, want application/json", ct)
			}
			var got openapi.Error
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got != tt.wantBody {
				t.Fatalf("got body %+v, want %+v", got, tt.wantBody)
			}
		})
	}
}

func TestResponseErrorLogsTheCause(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/invites/secret-token/accept", http.NoBody)
	req.Pattern = "POST /api/invites/{token}/accept"

	handler.ResponseError(logger)(rec, req, errors.New("database down"))

	var entry struct {
		Level  string `json:"level"`
		Method string `json:"method"`
		Route  string `json:"route"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", logs.String(), err)
	}
	want := struct {
		Level  string `json:"level"`
		Method string `json:"method"`
		Route  string `json:"route"`
		Error  string `json:"error"`
	}{Level: "ERROR", Method: http.MethodPost, Route: "POST /api/invites/{token}/accept", Error: "database down"}
	if entry != want {
		t.Fatalf("got log entry %+v, want %+v", entry, want)
	}
	if bytes.Contains(logs.Bytes(), []byte("secret-token")) {
		t.Fatalf("log entry %q holds the token of the path", logs.String())
	}
}

func TestResponseErrorMapsDomainErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		errs   []*domain.Error
	}{
		{
			status: http.StatusBadRequest,
			errs: []*domain.Error{
				domain.ErrInvalidEmail, domain.ErrInvalidName, domain.ErrInvalidPasskeyName,
				domain.ErrPasswordTooShort, domain.ErrPasswordTooLong, domain.ErrWrongCurrentPassword,
				domain.ErrPasskeyCeremonyExpired, domain.ErrPasskeyVerificationFailed,
			},
		},
		{status: http.StatusUnauthorized, errs: []*domain.Error{domain.ErrInvalidCredentials, domain.ErrUnauthenticated}},
		{status: http.StatusNotFound, errs: []*domain.Error{domain.ErrLinkNotFound, domain.ErrUserNotFound, domain.ErrPasskeyNotFound}},
		{
			status: http.StatusConflict,
			errs: []*domain.Error{
				domain.ErrEmailTaken, domain.ErrUserNotInvited, domain.ErrUserNotActive, domain.ErrCannotResetSelf,
			},
		},
		{status: http.StatusGone, errs: []*domain.Error{domain.ErrLinkUsed, domain.ErrLinkExpired}},
	}

	for _, tt := range tests {
		for _, domainErr := range tt.errs {
			t.Run(domainErr.Code, func(t *testing.T) {
				t.Parallel()

				var logs bytes.Buffer
				rec := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/x", http.NoBody)

				// Wrapped as a use case returns it.
				err := fmt.Errorf("use case: %w", domainErr)
				handler.ResponseError(slog.New(slog.NewJSONHandler(&logs, nil)))(rec, req, err)

				if rec.Code != tt.status {
					t.Fatalf("got status %d, want %d", rec.Code, tt.status)
				}
				var got openapi.Error
				if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if want := (openapi.Error{Code: domainErr.Code, Message: domainErr.Message}); got != want {
					t.Fatalf("got body %+v, want %+v", got, want)
				}
				if logs.Len() != 0 {
					t.Fatalf("a domain error was logged: %s", logs.String())
				}
			})
		}
	}
}

func TestResponseErrorAnswersInternalErrorAsPublic(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/me", http.NoBody)

	handler.ResponseError(slog.New(slog.DiscardHandler))(rec, req, domain.ErrSessionNotFound)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var got openapi.Error
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "unauthenticated" {
		t.Fatalf("got code %q, want unauthenticated", got.Code)
	}
}
