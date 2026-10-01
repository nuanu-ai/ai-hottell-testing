package domain_test

import (
	"errors"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestValidatePassword(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		wantErr error
	}{
		"8 characters":             {input: "abcdefgh"},
		"128 characters":           {input: strings.Repeat("a", 128)},
		"any composition":          {input: "        "},
		"128 runes over 128 bytes": {input: strings.Repeat("п", 128)},
		"empty":                    {input: "", wantErr: domain.ErrPasswordTooShort},
		"7 characters":             {input: "abcdefg", wantErr: domain.ErrPasswordTooShort},
		"7 runes over 8 bytes":     {input: strings.Repeat("п", 7), wantErr: domain.ErrPasswordTooShort},
		"129 characters":           {input: strings.Repeat("a", 129), wantErr: domain.ErrPasswordTooLong},
		"129 runes":                {input: strings.Repeat("п", 129), wantErr: domain.ErrPasswordTooLong},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := domain.ValidatePassword(tc.input); !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidatePassword() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
