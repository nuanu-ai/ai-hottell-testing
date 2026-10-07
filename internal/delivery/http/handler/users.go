package handler

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// ListUsers answers every user; isMe marks the user of the session.
func (a *API) ListUsers(ctx context.Context, _ openapi.ListUsersRequestObject) (openapi.ListUsersResponseObject, error) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	views, err := a.users.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]openapi.UserListItem, 0, len(views))
	for _, view := range views {
		items = append(items, userListItem(view, me))
	}
	return openapi.ListUsers200JSONResponse{Items: items}, nil
}

// InviteUser creates an invited user on behalf of the user of the session and answers it
// with the invitation link.
func (a *API) InviteUser(ctx context.Context, req openapi.InviteUserRequestObject) (openapi.InviteUserResponseObject, error) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	view, inviteURL, err := a.users.Invite(ctx, me, req.Body.Email, req.Body.Name)
	if err != nil {
		return nil, err
	}
	// The expiry is always set: the user was invited just now.
	return openapi.InviteUser201JSONResponse{
		User: userListItem(view, me), InviteUrl: inviteURL, ExpiresAt: *view.InviteExpiresAt,
	}, nil
}

// ReissueInvite issues a new invitation link to an invited user on behalf of the user of
// the session; the previous link stops working.
func (a *API) ReissueInvite(ctx context.Context, req openapi.ReissueInviteRequestObject) (
	openapi.ReissueInviteResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	inviteURL, expiresAt, err := a.users.ReissueInvite(ctx, me, req.Id)
	if err != nil {
		return nil, err
	}
	return openapi.ReissueInvite200JSONResponse{InviteUrl: inviteURL, ExpiresAt: expiresAt}, nil
}

// RevokeInvite deletes an invited user together with the invitation link.
func (a *API) RevokeInvite(ctx context.Context, req openapi.RevokeInviteRequestObject) (
	openapi.RevokeInviteResponseObject, error,
) {
	if _, _, err := signedIn(ctx); err != nil {
		return nil, err
	}
	if err := a.users.RevokeInvite(ctx, req.Id); err != nil {
		return nil, err
	}
	return openapi.RevokeInvite204Response{}, nil
}

// IssuePasswordReset issues a password reset link to another active user on behalf of
// the user of the session.
func (a *API) IssuePasswordReset(ctx context.Context, req openapi.IssuePasswordResetRequestObject) (
	openapi.IssuePasswordResetResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	resetURL, expiresAt, err := a.users.IssueReset(ctx, me, req.Id)
	if err != nil {
		return nil, err
	}
	return openapi.IssuePasswordReset200JSONResponse{ResetUrl: resetURL, ExpiresAt: expiresAt}, nil
}

// GetInvite answers the email and the name of the user an invitation link was issued to.
func (a *API) GetInvite(ctx context.Context, req openapi.GetInviteRequestObject) (openapi.GetInviteResponseObject, error) {
	email, name, err := a.users.LookupLink(ctx, req.Token, domain.LinkKindInvite)
	if err != nil {
		return nil, err
	}
	return openapi.GetInvite200JSONResponse{Email: email, Name: name}, nil
}

// AcceptInvite sets the first password by an invitation link and sets the cookie of the
// session it opens; the User-Agent of the request is kept with the session.
func (a *API) AcceptInvite(ctx context.Context, req openapi.AcceptInviteRequestObject) (
	openapi.AcceptInviteResponseObject, error,
) {
	token, expiresAt, err := a.users.AcceptInvite(ctx, req.Token, req.Body.Password, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	cookie := a.cookies.SessionCookie(token, expiresAt).String()
	return openapi.AcceptInvite204Response{Headers: openapi.AcceptInvite204ResponseHeaders{SetCookie: &cookie}}, nil
}

// GetPasswordReset answers the email and the name of the user a reset link was issued to.
func (a *API) GetPasswordReset(ctx context.Context, req openapi.GetPasswordResetRequestObject) (
	openapi.GetPasswordResetResponseObject, error,
) {
	email, name, err := a.users.LookupLink(ctx, req.Token, domain.LinkKindReset)
	if err != nil {
		return nil, err
	}
	return openapi.GetPasswordReset200JSONResponse{Email: email, Name: name}, nil
}

// CompletePasswordReset sets a new password by a reset link, which closes every session
// of the user, and sets the cookie of the session it opens; the User-Agent of the request
// is kept with the session.
func (a *API) CompletePasswordReset(ctx context.Context, req openapi.CompletePasswordResetRequestObject) (
	openapi.CompletePasswordResetResponseObject, error,
) {
	token, expiresAt, err := a.users.CompleteReset(ctx, req.Token, req.Body.Password, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	cookie := a.cookies.SessionCookie(token, expiresAt).String()
	return openapi.CompletePasswordReset204Response{
		Headers: openapi.CompletePasswordReset204ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// userListItem returns view as the users page lists it to the user with id me.
func userListItem(view domain.UserView, me uuid.UUID) openapi.UserListItem {
	return openapi.UserListItem{
		Id:              view.ID,
		Email:           view.Email.String(),
		Name:            view.Name.String(),
		Status:          openapi.UserListItemStatus(view.Status),
		CreatedAt:       view.CreatedAt,
		LastLoginAt:     view.LastLoginAt,
		InviteExpiresAt: view.InviteExpiresAt,
		IsMe:            view.ID == me,
	}
}

// userAgent returns the User-Agent header value, empty when the request came without one.
func userAgent(header *string) string {
	if header == nil {
		return ""
	}
	return *header
}
