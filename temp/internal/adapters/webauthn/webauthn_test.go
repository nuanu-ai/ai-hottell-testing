package webauthn_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/webauthn"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

const origin = "http://localhost:8080"

var errLookup = errors.New("lookup failed")

func newRelyingParty(t *testing.T) *webauthn.RelyingParty {
	t.Helper()

	rp, err := webauthn.New("localhost", []string{origin, "http://localhost:5173"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return rp
}

func testUser(t *testing.T) (domain.User, []byte) {
	t.Helper()

	email, err := domain.ParseEmail("ada@example.com")
	if err != nil {
		t.Fatalf("ParseEmail() error = %v", err)
	}
	name, err := domain.ParseUserName("Ада")
	if err != nil {
		t.Fatalf("ParseUserName() error = %v", err)
	}
	return domain.User{ID: uuid.New(), Email: email, Name: name, HasPassword: true}, bytes.Repeat([]byte{7}, 64)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	if _, err := webauthn.New("localhost", nil); err == nil {
		t.Fatal("New() without origins: want an error")
	}
	if _, err := webauthn.New("", []string{origin}); err == nil {
		t.Fatal("New() without RP ID: want an error")
	}
}

type creationOptions struct {
	Challenge string `json:"challenge"`
	Timeout   int    `json:"timeout"`
	RP        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"rp"`
	User struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"user"`
	AuthenticatorSelection struct {
		ResidentKey        string `json:"residentKey"`
		RequireResidentKey bool   `json:"requireResidentKey"`
		UserVerification   string `json:"userVerification"`
	} `json:"authenticatorSelection"`
	Attestation        string `json:"attestation"`
	ExcludeCredentials []struct {
		Type       string   `json:"type"`
		ID         string   `json:"id"`
		Transports []string `json:"transports"`
	} `json:"excludeCredentials"`
}

func TestBeginRegistration(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)
	existing := []domain.Passkey{
		{CredentialID: []byte{1, 2, 3}, Transports: []string{"usb"}},
		{CredentialID: []byte{4, 5, 6}, Transports: []string{"internal", "hybrid"}},
	}

	optionsJSON, sessionData, err := rp.BeginRegistration(user, handle, existing)
	if err != nil {
		t.Fatalf("BeginRegistration() error = %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(optionsJSON, &raw); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if _, ok := raw["publicKey"]; ok {
		t.Fatal("options are wrapped in publicKey, want its content")
	}

	var got creationOptions
	if err := json.Unmarshal(optionsJSON, &got); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if got.RP.ID != "localhost" || got.RP.Name != "Телеметрия агентов" {
		t.Errorf("rp = %+v, want localhost «Телеметрия агентов»", got.RP)
	}
	if !bytes.Equal(decode(t, got.User.ID), handle) {
		t.Errorf("user.id = %q, want the WebAuthn user handle", got.User.ID)
	}
	if got.User.Name != "ada@example.com" || got.User.DisplayName != "Ада" {
		t.Errorf("user = %+v, want email and name", got.User)
	}
	sel := got.AuthenticatorSelection
	if sel.ResidentKey != "required" || !sel.RequireResidentKey || sel.UserVerification != "preferred" {
		t.Errorf("authenticatorSelection = %+v, want residentKey required, userVerification preferred", sel)
	}
	if got.Attestation != "none" {
		t.Errorf("attestation = %q, want none", got.Attestation)
	}
	if got.Timeout != 300000 {
		t.Errorf("timeout = %d, want 300000 ms", got.Timeout)
	}
	if len(got.ExcludeCredentials) != len(existing) {
		t.Fatalf("excludeCredentials = %+v, want %d entries", got.ExcludeCredentials, len(existing))
	}
	for i, ex := range got.ExcludeCredentials {
		if ex.Type != "public-key" || !bytes.Equal(decode(t, ex.ID), existing[i].CredentialID) ||
			!slices.Equal(ex.Transports, existing[i].Transports) {
			t.Errorf("excludeCredentials[%d] = %+v, want passkey %v", i, ex, existing[i])
		}
	}

	var session struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(sessionData, &session); err != nil {
		t.Fatalf("decode session data: %v", err)
	}
	if session.Challenge == "" || session.Challenge != got.Challenge {
		t.Errorf("session challenge = %q, want the options challenge %q", session.Challenge, got.Challenge)
	}
}

func TestBeginRegistrationWithoutPasskeysExcludesNothing(t *testing.T) {
	t.Parallel()

	user, handle := testUser(t)
	optionsJSON, _, err := newRelyingParty(t).BeginRegistration(user, handle, nil)
	if err != nil {
		t.Fatalf("BeginRegistration() error = %v", err)
	}
	var got creationOptions
	if err := json.Unmarshal(optionsJSON, &got); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if len(got.ExcludeCredentials) != 0 {
		t.Errorf("excludeCredentials = %+v, want none", got.ExcludeCredentials)
	}
}

func TestBeginLogin(t *testing.T) {
	t.Parallel()

	optionsJSON, sessionData, err := newRelyingParty(t).BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}

	var got struct {
		Challenge          string            `json:"challenge"`
		Timeout            int               `json:"timeout"`
		RPID               string            `json:"rpId"`
		UserVerification   string            `json:"userVerification"`
		AllowCredentials   []json.RawMessage `json:"allowCredentials"`
		PublicKeyIsWrapped json.RawMessage   `json:"publicKey"`
	}
	if err := json.Unmarshal(optionsJSON, &got); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if got.PublicKeyIsWrapped != nil {
		t.Fatal("options are wrapped in publicKey, want its content")
	}
	if got.RPID != "localhost" || got.UserVerification != "preferred" || got.Timeout != 300000 {
		t.Errorf("options = %+v, want rpId localhost, userVerification preferred, timeout 300000", got)
	}
	if len(got.AllowCredentials) != 0 {
		t.Errorf("allowCredentials = %s, want empty", got.AllowCredentials)
	}

	var session struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(sessionData, &session); err != nil {
		t.Fatalf("decode session data: %v", err)
	}
	if session.Challenge == "" || session.Challenge != got.Challenge {
		t.Errorf("session challenge = %q, want the options challenge %q", session.Challenge, got.Challenge)
	}
}

// register runs a registration ceremony of user with auth and returns the new passkey.
func register(t *testing.T, rp *webauthn.RelyingParty, auth *authenticator, user domain.User, handle []byte) domain.Passkey {
	t.Helper()

	optionsJSON, sessionData, err := rp.BeginRegistration(user, handle, nil)
	if err != nil {
		t.Fatalf("BeginRegistration() error = %v", err)
	}
	passkey, err := rp.FinishRegistration(user, handle, nil, sessionData, auth.register(t, optionsJSON))
	if err != nil {
		t.Fatalf("FinishRegistration() error = %v", err)
	}
	return passkey
}

func TestFinishRegistration(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)
	auth := newAuthenticator(t, origin)

	got := register(t, rp, auth, user, handle)

	if got.UserID != user.ID || !bytes.Equal(got.CredentialID, auth.credentialID) {
		t.Errorf("passkey = %+v, want user %s credential %x", got, user.ID, auth.credentialID)
	}
	if len(got.PublicKey) == 0 || got.AttestationType == "" || len(got.AAGUID) != 16 {
		t.Errorf("passkey = %+v, want public key, attestation type and AAGUID", got)
	}
	if !slices.Equal(got.Transports, []string{"internal", "hybrid"}) {
		t.Errorf("transports = %v, want [internal hybrid]", got.Transports)
	}
	if !got.BackupEligible || !got.BackupState || got.SignCount != 0 {
		t.Errorf("passkey = %+v, want backed up with sign count 0", got)
	}
	if got.ID != uuid.Nil || got.Name != "" || !got.CreatedAt.IsZero() {
		t.Errorf("passkey = %+v, want no ID, name or timestamps", got)
	}
}

func TestFinishRegistrationRejectsResponses(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)

	optionsJSON, sessionData, err := rp.BeginRegistration(user, handle, nil)
	if err != nil {
		t.Fatalf("BeginRegistration() error = %v", err)
	}
	_, otherSession, err := rp.BeginRegistration(user, handle, nil)
	if err != nil {
		t.Fatalf("BeginRegistration() error = %v", err)
	}

	tests := []struct {
		name        string
		sessionData []byte
		response    []byte
	}{
		{name: "malformed", sessionData: sessionData, response: []byte(`{"id":"nope"}`)},
		{name: "foreign origin", sessionData: sessionData, response: newAuthenticator(t, "https://evil.example").register(t, optionsJSON)},
		{name: "another ceremony", sessionData: otherSession, response: newAuthenticator(t, origin).register(t, optionsJSON)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := rp.FinishRegistration(user, handle, nil, tt.sessionData, tt.response)
			if !errors.Is(err, domain.ErrPasskeyVerificationFailed) {
				t.Fatalf("FinishRegistration() error = %v, want %v", err, domain.ErrPasskeyVerificationFailed)
			}
		})
	}
}

func TestFinishLogin(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)
	auth := newAuthenticator(t, origin)
	passkey := register(t, rp, auth, user, handle)

	optionsJSON, sessionData, err := rp.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	var gotHandle, gotCredentialID []byte
	lookup := func(userHandle, credentialID []byte) (domain.User, []byte, []domain.Passkey, error) {
		gotHandle, gotCredentialID = userHandle, credentialID
		return user, handle, []domain.Passkey{passkey}, nil
	}

	userID, credentialID, signCount, backupState, err := rp.FinishLogin(sessionData, auth.login(t, optionsJSON), lookup)
	if err != nil {
		t.Fatalf("FinishLogin() error = %v", err)
	}
	if !bytes.Equal(gotHandle, handle) || !bytes.Equal(gotCredentialID, auth.credentialID) {
		t.Errorf("lookup got handle %x credential %x, want %x %x", gotHandle, gotCredentialID, handle, auth.credentialID)
	}
	if userID != user.ID || !bytes.Equal(credentialID, auth.credentialID) || signCount != 1 || !backupState {
		t.Errorf("FinishLogin() = %s %x %d %t, want %s %x 1 true",
			userID, credentialID, signCount, backupState, user.ID, auth.credentialID)
	}
}

func TestFinishLoginKeepsCounterOnCloneWarning(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)
	auth := newAuthenticator(t, origin)
	passkey := register(t, rp, auth, user, handle)
	// The stored counter is ahead of the one the authenticator signs, as for a clone.
	passkey.SignCount = 5

	optionsJSON, sessionData, err := rp.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	lookup := func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error) {
		return user, handle, []domain.Passkey{passkey}, nil
	}

	_, _, signCount, _, err := rp.FinishLogin(sessionData, auth.login(t, optionsJSON), lookup)
	if err != nil {
		t.Fatalf("FinishLogin() error = %v", err)
	}
	if signCount != 5 {
		t.Errorf("FinishLogin() signCount = %d, want the stored 5", signCount)
	}
}

func TestFinishLoginRejectsResponses(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)
	auth := newAuthenticator(t, origin)
	passkey := register(t, rp, auth, user, handle)
	other := newAuthenticator(t, origin)
	otherPasskey := register(t, rp, other, user, handle)

	optionsJSON, sessionData, err := rp.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	_, otherSession, err := rp.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	owns := func(passkeys ...domain.Passkey) func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error) {
		return func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error) {
			return user, handle, passkeys, nil
		}
	}

	tests := []struct {
		name        string
		sessionData []byte
		response    []byte
		lookup      func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error)
	}{
		{name: "malformed", sessionData: sessionData, response: []byte(`{"id":"nope"}`), lookup: owns(passkey)},
		{name: "deleted passkey", sessionData: sessionData, response: auth.login(t, optionsJSON), lookup: owns(otherPasskey)},
		{name: "another ceremony", sessionData: otherSession, response: auth.login(t, optionsJSON), lookup: owns(passkey)},
		{
			name: "foreign user handle", sessionData: sessionData, response: auth.login(t, optionsJSON),
			lookup: func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error) {
				return user, bytes.Repeat([]byte{9}, 64), []domain.Passkey{passkey}, nil
			},
		},
		{
			// The user handle is not signed: a lookup that finds the user by it and the
			// passkey by credential ID must not sign in with another user's passkey.
			name: "passkey of another user", sessionData: sessionData, response: auth.login(t, optionsJSON),
			lookup: func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error) {
				stranger := user
				stranger.ID = uuid.New()
				return stranger, handle, []domain.Passkey{passkey}, nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, _, err := rp.FinishLogin(tt.sessionData, tt.response, tt.lookup)
			if !errors.Is(err, domain.ErrPasskeyVerificationFailed) {
				t.Fatalf("FinishLogin() error = %v, want %v", err, domain.ErrPasskeyVerificationFailed)
			}
		})
	}
}

func TestFinishLoginReturnsLookupError(t *testing.T) {
	t.Parallel()

	rp := newRelyingParty(t)
	user, handle := testUser(t)
	auth := newAuthenticator(t, origin)
	register(t, rp, auth, user, handle)

	optionsJSON, sessionData, err := rp.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	lookup := func([]byte, []byte) (domain.User, []byte, []domain.Passkey, error) {
		return domain.User{}, nil, nil, errLookup
	}

	_, _, _, _, err = rp.FinishLogin(sessionData, auth.login(t, optionsJSON), lookup)
	if !errors.Is(err, errLookup) || errors.Is(err, domain.ErrPasskeyVerificationFailed) {
		t.Fatalf("FinishLogin() error = %v, want the lookup error as is", err)
	}
}
