package token_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/token"
	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
)

func TestIssuerNew(t *testing.T) {
	t.Parallel()

	var issuer token.Issuer
	tok, hash, err := issuer.New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 32 {
		t.Fatalf("New() token = %q, want 32 bytes in unpadded base64url", tok)
	}
	if want := sha256.Sum256(raw); !bytes.Equal(hash, want[:]) {
		t.Fatalf("New() hash = %x, want SHA-256 of the token bytes %x", hash, want)
	}

	rehashed, err := issuer.Hash(tok)
	if err != nil {
		t.Fatalf("Hash(New().token) error = %v", err)
	}
	if !bytes.Equal(rehashed, hash) {
		t.Fatalf("Hash(New().token) = %x, want New().hash %x", rehashed, hash)
	}
}

func TestIssuerNewUnique(t *testing.T) {
	t.Parallel()

	var issuer token.Issuer
	seen := make(map[string]bool)
	for range 1000 {
		tok, _, err := issuer.New()
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if seen[tok] {
			t.Fatalf("New() returned %q twice", tok)
		}
		seen[tok] = true
	}
}

func TestIssuerHashMalformed(t *testing.T) {
	t.Parallel()

	var issuer token.Issuer
	tok, _, err := issuer.New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// The last of 43 characters carries 4 data bits and 2 zero padding bits: flipping
	// a padding bit decodes to the same bytes unless decoding is strict.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	nonCanonical := tok[:42] + string(alphabet[strings.IndexByte(alphabet, tok[42])|1])

	tests := map[string]string{
		"empty":                   "",
		"truncated":               tok[:42],
		"extended":                tok + "A",
		"padded":                  tok + "=",
		"standard base64":         strings.NewReplacer("-", "+", "_", "/").Replace(tok[:41]) + "+/",
		"not base64":              "!" + tok[1:],
		"16 bytes":                base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		"33 bytes":                base64.RawURLEncoding.EncodeToString(make([]byte, 33)),
		"non-canonical last char": nonCanonical,
		"surrounding whitespace":  " " + tok + " ",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hash, err := issuer.Hash(input)
			if !errors.Is(err, auth.ErrMalformedToken) {
				t.Fatalf("Hash(%q) = %x, %v; want ErrMalformedToken", input, hash, err)
			}
		})
	}
}
