package argon2id_test

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"testing"

	"golang.org/x/crypto/argon2"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/argon2id"
)

// testMemoryKiB keeps each hash in tests to milliseconds.
const testMemoryKiB = 64

func TestHasherHashVerify(t *testing.T) {
	t.Parallel()

	h := argon2id.New(testMemoryKiB)
	hash, err := h.Hash("correct horse")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	tests := map[string]struct {
		password string
		want     bool
	}{
		"right password": {password: "correct horse", want: true},
		"wrong password": {password: "correct horsf", want: false},
		"empty password": {password: "", want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := h.Verify(tc.password, hash)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Verify() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasherHashFormat(t *testing.T) {
	t.Parallel()

	hash, err := argon2id.New(argon2id.DefaultMemoryKiB).Hash("correct horse")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	// 16-byte salt and 32-byte key in unpadded standard base64: 22 and 43 characters.
	format := regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !format.MatchString(hash) {
		t.Fatalf("Hash() = %q, want PHC argon2id with m=65536,t=3,p=4", hash)
	}
}

func TestHasherHashSaltsEveryCall(t *testing.T) {
	t.Parallel()

	h := argon2id.New(testMemoryKiB)
	first, err := h.Hash("correct horse")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	second, err := h.Hash("correct horse")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if first == second {
		t.Fatalf("Hash() returned %q twice, want a fresh salt per call", first)
	}
}

// TestHasherVerifyOtherParameters: a hash made with other parameters than the hasher's
// own still verifies, so changing the parameters later keeps old hashes working.
func TestHasherVerifyOtherParameters(t *testing.T) {
	t.Parallel()

	salt := []byte("another-salt-123")
	key := argon2.IDKey([]byte("correct horse"), salt, 1, 32, 1, 16)
	hash := fmt.Sprintf("$argon2id$v=19$m=32,t=1,p=1$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))

	h := argon2id.New(testMemoryKiB)
	for password, want := range map[string]bool{"correct horse": true, "wrong horse": false} {
		got, err := h.Verify(password, hash)
		if err != nil {
			t.Fatalf("Verify(%q) error = %v", password, err)
		}
		if got != want {
			t.Fatalf("Verify(%q) = %v, want %v", password, got, want)
		}
	}
}

func TestHasherVerifyMalformedHash(t *testing.T) {
	t.Parallel()

	const (
		salt = "c29tZXNhbHRzb21lc2FsdA"
		key  = "a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	)
	tests := map[string]string{
		"empty":               "",
		"bcrypt":              "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
		"argon2i":             "$argon2i$v=19$m=64,t=3,p=4$" + salt + "$" + key,
		"other version":       "$argon2id$v=16$m=64,t=3,p=4$" + salt + "$" + key,
		"missing version":     "$argon2id$m=64,t=3,p=4$" + salt + "$" + key,
		"missing key":         "$argon2id$v=19$m=64,t=3,p=4$" + salt,
		"extra field":         "$argon2id$v=19$m=64,t=3,p=4$" + salt + "$" + key + "$x",
		"parameters reorder":  "$argon2id$v=19$t=3,m=64,p=4$" + salt + "$" + key,
		"non-numeric memory":  "$argon2id$v=19$m=lots,t=3,p=4$" + salt + "$" + key,
		"zero time":           "$argon2id$v=19$m=64,t=0,p=4$" + salt + "$" + key,
		"zero threads":        "$argon2id$v=19$m=64,t=3,p=0$" + salt + "$" + key,
		"threads over 255":    "$argon2id$v=19$m=64,t=3,p=256$" + salt + "$" + key,
		"salt not base64":     "$argon2id$v=19$m=64,t=3,p=4$!!!!$" + key,
		"padded key":          "$argon2id$v=19$m=64,t=3,p=4$" + salt + "$" + key + "=",
		"empty salt":          "$argon2id$v=19$m=64,t=3,p=4$$" + key,
		"empty key":           "$argon2id$v=19$m=64,t=3,p=4$" + salt + "$",
		"trailing whitespace": "$argon2id$v=19$m=64,t=3,p=4$" + salt + "$" + key + " ",
	}

	h := argon2id.New(testMemoryKiB)
	for name, hash := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := h.Verify("correct horse", hash)
			if err == nil {
				t.Fatalf("Verify() = %v, nil error; want an error for a malformed hash", got)
			}
		})
	}
}
