package handler

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/middleware"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Login signs the user in with email and password and sets the cookie of the session
// it opens; the User-Agent of the request is kept with the session.
func (a *API) Login(ctx context.Context, req openapi.LoginRequestObject) (openapi.LoginResponseObject, error) {
	token, expiresAt, err := a.auth.LoginWithPassword(ctx, req.Body.Email, req.Body.Password, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	cookie := a.cookies.SessionCookie(token, expiresAt).String()
	return openapi.Login204Response{Headers: openapi.Login204ResponseHeaders{SetCookie: &cookie}}, nil
}

// Logout closes the session of the request and deletes its cookie.
func (a *API) Logout(ctx context.Context, _ openapi.LogoutRequestObject) (openapi.LogoutResponseObject, error) {
	_, sessionID, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.sessions.Close(ctx, sessionID); err != nil {
		return nil, err
	}
	cookie := a.cookies.ExpiredSessionCookie().String()
	return openapi.Logout204Response{Headers: openapi.Logout204ResponseHeaders{SetCookie: &cookie}}, nil
}

// GetMe answers the user of the session.
func (a *API) GetMe(ctx context.Context, _ openapi.GetMeRequestObject) (openapi.GetMeResponseObject, error) {
	userID, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	user, err := a.auth.Me(ctx, userID)
	if err != nil {
		return nil, err
	}
	return openapi.GetMe200JSONResponse{Id: user.ID, Email: user.Email.String(), Name: user.Name.String()}, nil
}

// ChangePassword replaces the password of the user of the session; the other sessions of
// the user are closed, this one stays open.
func (a *API) ChangePassword(ctx context.Context, req openapi.ChangePasswordRequestObject) (
	openapi.ChangePasswordResponseObject, error,
) {
	userID, sessionID, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.auth.ChangePassword(ctx, userID, sessionID, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return openapi.ChangePassword204Response{}, nil
}

// signedIn returns the user and the session the request came with;
// domain.ErrUnauthenticated when it came without one.
func signedIn(ctx context.Context) (uuid.UUID, uuid.UUID, error) {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, domain.ErrUnauthenticated
	}
	sessionID, ok := middleware.SessionID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, domain.ErrUnauthenticated
	}
	return userID, sessionID, nil
}
