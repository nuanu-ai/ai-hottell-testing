// Package token issues one-time tokens: 32 random bytes in unpadded base64url, stored
// as their SHA-256.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
)

const size = 32

// encoding is strict so that non-zero padding bits are rejected rather than dropped.
func encoding() *base64.Encoding {
	return base64.RawURLEncoding.Strict()
}

var _ auth.TokenIssuer = Issuer{}

// Issuer issues tokens from crypto/rand.
type Issuer struct{}

// New returns a fresh token and its SHA-256.
func (Issuer) New() (string, []byte, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("read token: %w", err)
	}
	return encoding().EncodeToString(raw), hash(raw), nil
}

// Hash returns the SHA-256 of token, or auth.ErrMalformedToken when token is not
// 32 bytes in unpadded base64url.
func (Issuer) Hash(token string) ([]byte, error) {
	raw, err := encoding().DecodeString(token)
	if err != nil || len(raw) != size {
		return nil, auth.ErrMalformedToken
	}
	return hash(raw), nil
}

func hash(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}
