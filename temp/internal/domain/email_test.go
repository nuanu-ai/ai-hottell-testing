package domain_test

import (
	"errors"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestParseEmail(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		want    domain.Email
		wantErr error
	}{
		"plain":                      {input: "user@example.com", want: "user@example.com"},
		"trimmed and lower-cased":    {input: "  User@Example.COM\t", want: "user@example.com"},
		"localhost domain":           {input: "admin@localhost", want: "admin@localhost"},
		"localhost upper-cased":      {input: "Admin@LOCALHOST", want: "admin@localhost"},
		"shortest":                   {input: "a@.", want: "a@."},
		"longest 254":                {input: strings.Repeat("a", 242) + "@example.com", want: domain.Email(strings.Repeat("a", 242) + "@example.com")},
		"non-ascii counted by runes": {input: "пользователь@пример.рф", want: "пользователь@пример.рф"},
		"empty":                      {input: "", wantErr: domain.ErrInvalidEmail},
		"only spaces":                {input: "   ", wantErr: domain.ErrInvalidEmail},
		"too short":                  {input: "@.", wantErr: domain.ErrInvalidEmail},
		"too long 255":               {input: strings.Repeat("a", 243) + "@example.com", wantErr: domain.ErrInvalidEmail},
		"no at":                      {input: "user.example.com", wantErr: domain.ErrInvalidEmail},
		"two ats":                    {input: "user@host@example.com", wantErr: domain.ErrInvalidEmail},
		"empty local part":           {input: "@example.com", wantErr: domain.ErrInvalidEmail},
		"empty domain":               {input: "user@", wantErr: domain.ErrInvalidEmail},
		"domain without dot":         {input: "user@example", wantErr: domain.ErrInvalidEmail},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParseEmail(tc.input)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParseEmail(%q) error = %v, want %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ParseEmail(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestEmail_String(t *testing.T) {
	t.Parallel()

	if got := domain.Email("user@example.com").String(); got != "user@example.com" {
		t.Fatalf("String() = %q, want %q", got, "user@example.com")
	}
}
