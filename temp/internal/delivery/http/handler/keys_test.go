package handler_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/application/keys"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/handler/mock"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// mcpURL is the address of the MCP server the test service answers an issued key with.
const mcpURL = "http://localhost:8080/mcp"

// keysServer serves the contract on keys.
func keysServer(t *testing.T, keys *mock.MockKeys) http.Handler {
	t.Helper()
	ctrl := gomock.NewController(t)
	return serve(mock.NewMockAuth(ctrl), mock.NewMockSessions(ctrl), mock.NewMockUsers(ctrl),
		mock.NewMockPasskeys(ctrl), keys, nil, slog.New(slog.DiscardHandler))
}

func TestGetMyKeys(t *testing.T) {
	t.Parallel()

	usedAt := now.Add(-time.Minute)
	tests := []struct {
		name   string
		status keys.Status
		want   string
	}{
		{
			name: "both active",
			status: keys.Status{
				MCP:    keys.KeyStatus{Active: true, CreatedAt: now.Add(-time.Hour), LastUsedAt: &usedAt},
				Ingest: keys.KeyStatus{Active: true, CreatedAt: now.Add(-2 * time.Hour)},
			},
			want: `{"ingest":{"active":true,"createdAt":"2026-09-30T10:00:00Z","lastUsedAt":null},` +
				`"mcp":{"active":true,"createdAt":"2026-09-30T11:00:00Z","lastUsedAt":"2026-09-30T11:59:00Z"}}`,
		},
		{
			name: "none",
			want: `{"ingest":{"active":false,"createdAt":null,"lastUsedAt":null},` +
				`"mcp":{"active":false,"createdAt":null,"lastUsedAt":null}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keysService := mock.NewMockKeys(gomock.NewController(t))
			keysService.EXPECT().KeysStatus(gomock.Any(), userID).Return(tt.status, nil)

			rec := do(t, keysServer(t, keysService), http.MethodGet, "/me/keys", "", true)

			wantBody(t, rec, http.StatusOK, tt.want)
		})
	}
}

func TestIssueMyMcpKey(t *testing.T) {
	t.Parallel()

	keysService := mock.NewMockKeys(gomock.NewController(t))
	keysService.EXPECT().IssueMCPKey(gomock.Any(), userID).Return("synthetic-mcp-key", nil)

	rec := do(t, keysServer(t, keysService), http.MethodPost, "/me/keys/mcp", "", true)

	wantBody(t, rec, http.StatusCreated,
		`{"key":"synthetic-mcp-key","mcpUrl":"`+mcpURL+`","serverName":"hottell"}`)
}

func TestRevokeMyMcpKey(t *testing.T) {
	t.Parallel()

	keysService := mock.NewMockKeys(gomock.NewController(t))
	keysService.EXPECT().RevokeMCPKey(gomock.Any(), userID).Return(nil)

	rec := do(t, keysServer(t, keysService), http.MethodDelete, "/me/keys/mcp", "", true)

	wantBody(t, rec, http.StatusNoContent, "")
}

func TestRevokeMyMcpKeyNone(t *testing.T) {
	t.Parallel()

	keysService := mock.NewMockKeys(gomock.NewController(t))
	keysService.EXPECT().RevokeMCPKey(gomock.Any(), userID).
		Return(fmt.Errorf("revoke mcp key: %w", domain.ErrAccessKeyNotFound))

	rec := do(t, keysServer(t, keysService), http.MethodDelete, "/me/keys/mcp", "", true)

	wantError(t, rec, http.StatusNotFound, domain.ErrAccessKeyNotFound)
}

func TestReissueMyIngestToken(t *testing.T) {
	t.Parallel()

	keysService := mock.NewMockKeys(gomock.NewController(t))
	keysService.EXPECT().ReissueIngestToken(gomock.Any(), userID).Return("synthetic-ingest-token", nil)

	rec := do(t, keysServer(t, keysService), http.MethodPost, "/me/keys/ingest/reissue", "", true)

	// The open value reaches only the binary, through MCP.
	wantBody(t, rec, http.StatusNoContent, "")
}

func TestKeysFail(t *testing.T) {
	t.Parallel()

	failure := errors.New("database down")
	tests := []struct {
		name   string
		method string
		path   string
		expect func(k *mock.MockKeysMockRecorder)
	}{
		{
			name: "status", method: http.MethodGet, path: "/me/keys",
			expect: func(k *mock.MockKeysMockRecorder) {
				k.KeysStatus(gomock.Any(), userID).Return(keys.Status{}, failure)
			},
		},
		{
			name: "issue mcp key", method: http.MethodPost, path: "/me/keys/mcp",
			expect: func(k *mock.MockKeysMockRecorder) { k.IssueMCPKey(gomock.Any(), userID).Return("", failure) },
		},
		{
			name: "revoke mcp key", method: http.MethodDelete, path: "/me/keys/mcp",
			expect: func(k *mock.MockKeysMockRecorder) { k.RevokeMCPKey(gomock.Any(), userID).Return(failure) },
		},
		{
			name: "reissue ingest token", method: http.MethodPost, path: "/me/keys/ingest/reissue",
			expect: func(k *mock.MockKeysMockRecorder) {
				k.ReissueIngestToken(gomock.Any(), userID).Return("", failure)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keysService := mock.NewMockKeys(gomock.NewController(t))
			tt.expect(keysService.EXPECT())

			rec := do(t, keysServer(t, keysService), tt.method, tt.path, "", true)

			wantBody(t, rec, http.StatusInternalServerError,
				`{"code":"internal","message":"Что-то пошло не так. Попробуйте ещё раз"}`)
		})
	}
}

func TestKeysNeedSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/me/keys"},
		{method: http.MethodPost, path: "/me/keys/mcp"},
		{method: http.MethodDelete, path: "/me/keys/mcp"},
		{method: http.MethodPost, path: "/me/keys/ingest/reissue"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			// The mock expects no call: a request without a session never reaches the keys.
			rec := do(t, keysServer(t, mock.NewMockKeys(gomock.NewController(t))), tt.method, tt.path, "", false)

			wantError(t, rec, http.StatusUnauthorized, domain.ErrUnauthenticated)
		})
	}
}
