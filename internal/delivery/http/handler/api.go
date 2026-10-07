package handler

import (
	"context"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

var _ openapi.StrictServerInterface = (*API)(nil)

// API implements the operations of the OpenAPI contract.
type API struct {
	version  string
	auth     Auth
	sessions Sessions
	users    Users
	passkeys Passkeys
	keys     Keys
	settings Settings
	cookies  *middleware.Cookies
	mcpURL   string
	// analytics is nil while the server has no telemetry store: its operations answer 503.
	analytics Analytics
	// review is nil while the Deep review is not wired: its operations answer 503.
	review Review
}

// NewAPI returns an API that reports the given build version, signs users in and out
// through auth and sessions, manages users through users, runs the passkey ceremonies
// through passkeys, manages the access keys through keys and the deny settings through
// settings, and sets the session and ceremony cookies with cookies; mcpURL is the address of the MCP server an issued MCP
// key is answered with.
func NewAPI(
	version string, auth Auth, sessions Sessions, users Users, passkeys Passkeys, keys Keys,
	settings Settings, cookies *middleware.Cookies, mcpURL string,
) *API {
	return &API{
		version: version, auth: auth, sessions: sessions, users: users, passkeys: passkeys, keys: keys,
		settings: settings, cookies: cookies, mcpURL: mcpURL,
	}
}

// GetVersion answers the version the service was built with.
func (a *API) GetVersion(context.Context, openapi.GetVersionRequestObject) (openapi.GetVersionResponseObject, error) {
	return openapi.GetVersion200JSONResponse{Version: a.version}, nil
}
