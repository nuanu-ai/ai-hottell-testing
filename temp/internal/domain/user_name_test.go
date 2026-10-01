package domain_test

import (
	"errors"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestParseUserName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		want    domain.UserName
		wantErr error
	}{
		"plain":                 {input: "Alva", want: "Alva"},
		"trimmed":               {input: "  Alva Doe \n", want: "Alva Doe"},
		"one character":         {input: "A", want: "A"},
		"100 runes":             {input: strings.Repeat("я", 100), want: domain.UserName(strings.Repeat("я", 100))},
		"100 runes with spaces": {input: " " + strings.Repeat("a", 100) + " ", want: domain.UserName(strings.Repeat("a", 100))},
		"empty":                 {input: "", wantErr: domain.ErrInvalidName},
		"only spaces":           {input: " \t ", wantErr: domain.ErrInvalidName},
		"101 runes":             {input: strings.Repeat("я", 101), wantErr: domain.ErrInvalidName},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParseUserName(tc.input)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParseUserName(%q) error = %v, want %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ParseUserName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestUserName_String(t *testing.T) {
	t.Parallel()

	if got := domain.UserName("Alva").String(); got != "Alva" {
		t.Fatalf("String() = %q, want %q", got, "Alva")
	}
}
