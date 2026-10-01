package domain_test

import (
	"errors"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestParsePasskeyName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		want    domain.PasskeyName
		wantErr error
	}{
		"plain":         {input: "MacBook", want: "MacBook"},
		"trimmed":       {input: "  Ноутбук  ", want: "Ноутбук"},
		"one character": {input: "M", want: "M"},
		"64 runes":      {input: strings.Repeat("ж", 64), want: domain.PasskeyName(strings.Repeat("ж", 64))},
		"empty":         {input: "", wantErr: domain.ErrInvalidPasskeyName},
		"only spaces":   {input: "   ", wantErr: domain.ErrInvalidPasskeyName},
		"65 runes":      {input: strings.Repeat("ж", 65), wantErr: domain.ErrInvalidPasskeyName},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParsePasskeyName(tc.input)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParsePasskeyName(%q) error = %v, want %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ParsePasskeyName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPasskeyName_String(t *testing.T) {
	t.Parallel()

	if got := domain.PasskeyName("MacBook").String(); got != "MacBook" {
		t.Fatalf("String() = %q, want %q", got, "MacBook")
	}
}
