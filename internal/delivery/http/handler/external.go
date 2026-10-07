package handler

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/analytics"
	"git.alva.dev/alva/harness-telemetry/internal/application/keys"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Auth,Sessions,Users,Passkeys,Keys,Settings,Releases,Analytics

// Auth runs the sign-in use cases.
type Auth interface {
	// LoginWithPassword signs the user with email and password in and returns the token
	// of the session it opens and its expiry; domain.ErrInvalidCredentials on a mismatch.
	LoginWithPassword(ctx context.Context, email, password, userAgent string) (string, time.Time, error)
	// Me returns the signed-in user with userID.
	Me(ctx context.Context, userID uuid.UUID) (domain.User, error)
	// ChangePassword replaces the password of the user with userID once current matches
	// it, closing every other session of the user but sessionID.
	ChangePassword(ctx context.Context, userID, sessionID uuid.UUID, current, next string) error
}

// Sessions closes the sessions of signed-in browsers.
type Sessions interface {
	// Close closes the session with sessionID.
	Close(ctx context.Context, sessionID uuid.UUID) error
}

// Users runs the user management use cases of the users page and of the holders of
// invitation and password reset links.
type Users interface {
	// List returns every user; an invited one carries the expiry of the unused invitation.
	List(ctx context.Context) ([]domain.UserView, error)
	// Invite creates an invited user with email and name on behalf of actorID and returns
	// the user and the URL of the invitation link.
	Invite(ctx context.Context, actorID uuid.UUID, email, name string) (domain.UserView, string, error)
	// ReissueInvite issues a new invitation link to the invited user with userID and returns
	// its URL and expiry.
	ReissueInvite(ctx context.Context, actorID, userID uuid.UUID) (string, time.Time, error)
	// RevokeInvite deletes the invited user with userID.
	RevokeInvite(ctx context.Context, userID uuid.UUID) error
	// IssueReset issues a password reset link to the active user with userID and returns
	// its URL and expiry.
	IssueReset(ctx context.Context, actorID, userID uuid.UUID) (string, time.Time, error)
	// LookupLink returns the email and the name of the user a link of kind that token opens
	// was issued to.
	LookupLink(ctx context.Context, token string, kind domain.LinkKind) (string, string, error)
	// AcceptInvite sets password by the invitation link token and returns the token of the
	// session it opens and its expiry.
	AcceptInvite(ctx context.Context, token, password, userAgent string) (string, time.Time, error)
	// CompleteReset sets password by the reset link token, closing every session of the
	// user, and returns the token of the session it opens and its expiry.
	CompleteReset(ctx context.Context, token, password, userAgent string) (string, time.Time, error)
}

// Passkeys runs the passkey use cases: signing in with a passkey and managing the
// passkeys of the signed-in user. A ceremony begun by BeginLogin or BeginRegistration is
// finished once within five minutes; a missing, expired or finished one fails its finish
// with domain.ErrCeremonyNotFound, a browser answer that does not verify with
// domain.ErrPasskeyVerificationFailed.
type Passkeys interface {
	// BeginLogin starts a sign-in with any discoverable passkey and returns the ceremony
	// and the options for the browser.
	BeginLogin(ctx context.Context) (uuid.UUID, []byte, error)
	// FinishLogin verifies responseJSON of the browser against the login ceremony with
	// ceremonyID and returns the token of the session it opens and its expiry.
	FinishLogin(ctx context.Context, ceremonyID uuid.UUID, responseJSON []byte, userAgent string) (string, time.Time, error)
	// BeginRegistration starts adding a passkey named name to the user with userID and
	// returns the ceremony and the options for the browser.
	BeginRegistration(ctx context.Context, userID uuid.UUID, name string) (uuid.UUID, []byte, error)
	// FinishRegistration verifies responseJSON of the browser against the registration
	// ceremony with ceremonyID of the user with userID and returns the passkey it stores.
	FinishRegistration(ctx context.Context, userID, ceremonyID uuid.UUID, responseJSON []byte) (domain.Passkey, error)
	// List returns the passkeys of the user with userID, oldest first.
	List(ctx context.Context, userID uuid.UUID) ([]domain.Passkey, error)
	// Delete removes the passkey with passkeyID of the user with userID;
	// domain.ErrPasskeyNotFound when the user has no such passkey.
	Delete(ctx context.Context, userID, passkeyID uuid.UUID) error
}

// Keys runs the use cases of the access keys of the signed-in user: the MCP key and the
// collector token of the hottell binary.
type Keys interface {
	// KeysStatus returns whether the user with userID has an active key of each kind, when
	// it was created and when it was last used.
	KeysStatus(ctx context.Context, userID uuid.UUID) (keys.Status, error)
	// IssueMCPKey issues a new MCP key of the user with userID, revoking the one they had,
	// and returns its open value.
	IssueMCPKey(ctx context.Context, userID uuid.UUID) (string, error)
	// RevokeMCPKey revokes the MCP key of the user with userID;
	// domain.ErrAccessKeyNotFound when there is none.
	RevokeMCPKey(ctx context.Context, userID uuid.UUID) error
	// ReissueIngestToken issues a new collector token of the user with userID, revoking the
	// one they had, and returns its open value.
	ReissueIngestToken(ctx context.Context, userID uuid.UUID) (string, error)
}

// Settings runs the use cases of the deny settings of the signed-in user.
type Settings interface {
	// Get returns the settings of the user with userID in full form and their version; the
	// defaults and version 0 when the user has never saved settings.
	Get(ctx context.Context, userID uuid.UUID) (domain.TelemetrySettings, int64, error)
	// Update checks document against the settings schema, stores it as the settings of the
	// user with userID in place of version expectedVersion and returns the version the
	// settings now have; domain.ErrInvalidTelemetrySettings when the document breaks the
	// schema, domain.ErrSettingsVersionConflict when the settings are no longer at
	// expectedVersion.
	Update(ctx context.Context, userID uuid.UUID, document json.RawMessage, expectedVersion int64) (int64, error)
}

// Releases opens the files of the latest hottell release.
type Releases interface {
	// Open returns the content of the file name of the latest hottell release and its size,
	// -1 when unknown; the caller closes the content. It fails when there is no such
	// release or file, or the releases cannot be read.
	Open(ctx context.Context, name string) (io.ReadCloser, int64, error)
}

// Analytics runs the live analytics: the dataset of a filter, a session's timeline, the pulse of
// the hooks, the delivery status of a person and the topics each person hid. Every signed-in
// user sees every user; a user given is a filter, not an access check.
type Analytics interface {
	// Dataset returns the dataset of the filter; a *domain.InvalidFilterError names a parameter
	// out of range.
	Dataset(ctx context.Context, f analytics.Filter) (analytics.Dataset, error)
	// Team returns «Команда» for the filter; a *domain.InvalidFilterError names a parameter out of
	// range.
	Team(ctx context.Context, f analytics.TeamFilter) (analytics.Team, error)
	// TeamPulse returns the pulse of «Команда»: each person's hook events by hour over the 5
	// whole UTC days before today; telemetry.ErrUnavailable when the store cannot be read.
	TeamPulse(ctx context.Context) (analytics.TeamPulse, error)
	// Session returns the timeline of the session sid; domain.ErrAnalyticsSessionNotFound or
	// domain.ErrAnalyticsSessionAmbiguous.
	Session(ctx context.Context, sid, agent string, userID *uuid.UUID) (analytics.SessionTimeline, error)
	// Pulse returns the pulse of the person and agent when given; a read failure is in the
	// pulse's Error.
	Pulse(userID *uuid.UUID, agent string) (analytics.Pulse, error)
	// Delivery returns how sending works for the person userID.
	Delivery(ctx context.Context, userID uuid.UUID) (analytics.Delivery, error)
	// HiddenTopics returns the topics the person userID hid.
	HiddenTopics(ctx context.Context, userID uuid.UUID) ([]string, error)
	// HideTopic hides the topic key for the person userID; domain.ErrInvalidTopicKey.
	HideTopic(ctx context.Context, userID uuid.UUID, key string) error
	// UnhideTopic shows the topic key to the person userID again; domain.ErrInvalidTopicKey.
	UnhideTopic(ctx context.Context, userID uuid.UUID, key string) error
	// UnhideAllTopics shows every topic the person userID hid again.
	UnhideAllTopics(ctx context.Context, userID uuid.UUID) error
}
