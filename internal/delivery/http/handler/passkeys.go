package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// BeginPasskeyLogin starts a sign-in with any discoverable passkey, no email asked, and
// answers the options for the browser with the ceremony cookie.
func (a *API) BeginPasskeyLogin(ctx context.Context, _ openapi.BeginPasskeyLoginRequestObject) (
	openapi.BeginPasskeyLoginResponseObject, error,
) {
	ceremonyID, optionsJSON, err := a.passkeys.BeginLogin(ctx)
	if err != nil {
		return nil, err
	}
	return a.optionsResponse(ceremonyID, optionsJSON), nil
}

// FinishPasskeyLogin verifies the answer of the browser against the ceremony of the
// cookie and sets the cookie of the session it opens; the User-Agent of the request is
// kept with the session. middleware.EndCeremony deletes the ceremony cookie.
func (a *API) FinishPasskeyLogin(ctx context.Context, req openapi.FinishPasskeyLoginRequestObject) (
	openapi.FinishPasskeyLoginResponseObject, error,
) {
	ceremonyID, err := ceremonyOf(req.Params.HtWebauthn)
	if err != nil {
		return nil, err
	}
	credential, err := json.Marshal(req.Body.Credential)
	if err != nil {
		return nil, fmt.Errorf("encode credential: %w", err)
	}
	token, expiresAt, err := a.passkeys.FinishLogin(ctx, ceremonyID, credential, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	return cookiesResponse{status: http.StatusNoContent, cookies: []*http.Cookie{a.cookies.SessionCookie(token, expiresAt)}}, nil
}

// ListMyPasskeys answers the passkeys of the user of the session, oldest first.
func (a *API) ListMyPasskeys(ctx context.Context, _ openapi.ListMyPasskeysRequestObject) (
	openapi.ListMyPasskeysResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	passkeys, err := a.passkeys.List(ctx, me)
	if err != nil {
		return nil, err
	}
	items := make([]openapi.PasskeyItem, 0, len(passkeys))
	for _, p := range passkeys {
		items = append(items, passkeyItem(p))
	}
	return openapi.ListMyPasskeys200JSONResponse{Items: items}, nil
}

// BeginPasskeyRegistration starts adding a named passkey to the user of the session and
// answers the options for the browser with the ceremony cookie.
func (a *API) BeginPasskeyRegistration(ctx context.Context, req openapi.BeginPasskeyRegistrationRequestObject) (
	openapi.BeginPasskeyRegistrationResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	ceremonyID, optionsJSON, err := a.passkeys.BeginRegistration(ctx, me, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return a.optionsResponse(ceremonyID, optionsJSON), nil
}

// FinishPasskeyRegistration verifies the answer of the browser against the ceremony of
// the cookie and answers the passkey it adds to the user of the session.
// middleware.EndCeremony deletes the ceremony cookie.
func (a *API) FinishPasskeyRegistration(ctx context.Context, req openapi.FinishPasskeyRegistrationRequestObject) (
	openapi.FinishPasskeyRegistrationResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	ceremonyID, err := ceremonyOf(req.Params.HtWebauthn)
	if err != nil {
		return nil, err
	}
	credential, err := json.Marshal(req.Body.Credential)
	if err != nil {
		return nil, fmt.Errorf("encode credential: %w", err)
	}
	passkey, err := a.passkeys.FinishRegistration(ctx, me, ceremonyID, credential)
	if err != nil {
		return nil, err
	}
	return cookiesResponse{status: http.StatusCreated, body: passkeyItem(passkey)}, nil
}

// DeleteMyPasskey removes a passkey of the user of the session; the password stays.
func (a *API) DeleteMyPasskey(ctx context.Context, req openapi.DeleteMyPasskeyRequestObject) (
	openapi.DeleteMyPasskeyResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.passkeys.Delete(ctx, me, req.Id); err != nil {
		return nil, err
	}
	return openapi.DeleteMyPasskey204Response{}, nil
}

// optionsResponse answers the options of a begun ceremony and sets its cookie.
func (a *API) optionsResponse(ceremonyID uuid.UUID, optionsJSON []byte) cookiesResponse {
	return cookiesResponse{
		status:  http.StatusOK,
		cookies: []*http.Cookie{a.cookies.CeremonyCookie(ceremonyID.String())},
		body:    passkeyOptions{Options: json.RawMessage(optionsJSON)},
	}
}

// ceremonyOf returns the ceremony the cookie value names; domain.ErrCeremonyNotFound when
// the request came without the cookie or with one that names no ceremony.
func ceremonyOf(cookie *openapi.WebAuthnCeremony) (uuid.UUID, error) {
	if cookie == nil {
		return uuid.Nil, domain.ErrCeremonyNotFound
	}
	ceremonyID, err := uuid.Parse(*cookie)
	if err != nil {
		return uuid.Nil, domain.ErrCeremonyNotFound
	}
	return ceremonyID, nil
}

// passkeyItem returns p as the profile lists it.
func passkeyItem(p domain.Passkey) openapi.PasskeyItem {
	return openapi.PasskeyItem{Id: p.ID, Name: p.Name.String(), CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
}

// passkeyOptions is the PasskeyOptions body with the options passed through as the
// relying party encoded them.
type passkeyOptions struct {
	Options json.RawMessage `json:"options"`
}

// cookiesResponse answers status with body as JSON, none when body is nil, adding each
// of cookies. The generated responses carry one Set-Cookie and set it in place of any
// other, which would drop a second cookie and the renewed session cookie.
type cookiesResponse struct {
	status  int
	cookies []*http.Cookie
	body    any
}

func (r cookiesResponse) visit(w http.ResponseWriter) error {
	for _, c := range r.cookies {
		http.SetCookie(w, c)
	}
	if r.body == nil {
		w.WriteHeader(r.status)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.status)
	return json.NewEncoder(w).Encode(r.body)
}

func (r cookiesResponse) VisitBeginPasskeyLoginResponse(w http.ResponseWriter) error {
	return r.visit(w)
}

func (r cookiesResponse) VisitFinishPasskeyLoginResponse(w http.ResponseWriter) error {
	return r.visit(w)
}

func (r cookiesResponse) VisitBeginPasskeyRegistrationResponse(w http.ResponseWriter) error {
	return r.visit(w)
}

func (r cookiesResponse) VisitFinishPasskeyRegistrationResponse(w http.ResponseWriter) error {
	return r.visit(w)
}
