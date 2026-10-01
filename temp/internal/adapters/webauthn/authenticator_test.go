package webauthn_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// Authenticator data flags, WebAuthn §6.1.
const (
	flagUserPresent    = 0x01
	flagUserVerified   = 0x04
	flagBackupEligible = 0x08
	flagBackupState    = 0x10
	flagAttestedData   = 0x40
)

// authenticator is a software passkey authenticator: one discoverable ES256 credential
// with none attestation, synced (backup eligible and backed up).
type authenticator struct {
	origin       string
	key          *ecdsa.PrivateKey
	credentialID []byte
	userHandle   []byte
	signCount    uint32
}

func newAuthenticator(t *testing.T, origin string) *authenticator {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	credentialID := make([]byte, 16)
	if _, err := rand.Read(credentialID); err != nil {
		t.Fatalf("read credential id: %v", err)
	}
	return &authenticator{origin: origin, key: key, credentialID: credentialID}
}

// register answers creation options with an attestation response, keeping the user handle.
func (a *authenticator) register(t *testing.T, optionsJSON []byte) []byte {
	t.Helper()

	var options struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(optionsJSON, &options); err != nil {
		t.Fatalf("decode creation options: %v", err)
	}
	a.userHandle = decode(t, options.User.ID)

	x, y := a.publicKeyXY(t)
	publicKey := marshalCBOR(t, map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})

	authData := a.authData(options.RP.ID, flagUserPresent|flagUserVerified|flagBackupEligible|flagBackupState|flagAttestedData)
	authData = append(authData, make([]byte, 16)...) // zero AAGUID
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(a.credentialID)))
	authData = append(authData, a.credentialID...)
	authData = append(authData, publicKey...)

	attestationObject := marshalCBOR(t, map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
	clientData := a.clientData(t, "webauthn.create", options.Challenge)

	return marshalJSON(t, map[string]any{
		"id":    encode(a.credentialID),
		"rawId": encode(a.credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    encode(clientData),
			"attestationObject": encode(attestationObject),
			"transports":        []string{"internal", "hybrid"},
		},
	})
}

// login answers request options with an assertion signed by the credential.
func (a *authenticator) login(t *testing.T, optionsJSON []byte) []byte {
	t.Helper()

	var options struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
	}
	if err := json.Unmarshal(optionsJSON, &options); err != nil {
		t.Fatalf("decode request options: %v", err)
	}

	a.signCount++
	authData := a.authData(options.RPID, flagUserPresent|flagUserVerified|flagBackupEligible|flagBackupState)
	clientData := a.clientData(t, "webauthn.get", options.Challenge)
	clientDataHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientDataHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}

	return marshalJSON(t, map[string]any{
		"id":    encode(a.credentialID),
		"rawId": encode(a.credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    encode(clientData),
			"authenticatorData": encode(authData),
			"signature":         encode(signature),
			"userHandle":        encode(a.userHandle),
		},
	})
}

func (a *authenticator) authData(rpID string, flags byte) []byte {
	rpIDHash := sha256.Sum256([]byte(rpID))
	data := append(rpIDHash[:], flags)
	return binary.BigEndian.AppendUint32(data, a.signCount)
}

func (a *authenticator) clientData(t *testing.T, ceremony, challenge string) []byte {
	t.Helper()
	return marshalJSON(t, map[string]any{"type": ceremony, "challenge": challenge, "origin": a.origin})
}

func (a *authenticator) publicKeyXY(t *testing.T) (x, y []byte) {
	t.Helper()

	point, err := a.key.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("encode public key: %v", err)
	}
	// Uncompressed point: 0x04 || X || Y.
	return point[1:33], point[33:]
}

func marshalCBOR(t *testing.T, v any) []byte {
	t.Helper()

	data, err := webauthncbor.Marshal(v)
	if err != nil {
		t.Fatalf("encode cbor: %v", err)
	}
	return data
}

func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode json: %v", err)
	}
	return data
}

func encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decode(t *testing.T, s string) []byte {
	t.Helper()

	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode base64url %q: %v", s, err)
	}
	return b
}
