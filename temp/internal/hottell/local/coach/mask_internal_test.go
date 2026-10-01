package coach

import (
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	t.Parallel()
	mask := newMasker()
	for in, want := range map[string]string{
		"api_key=sk-abcdefghijklmnop":         "api_key=[скрыто]",
		"Authorization: Bearer abcdef123456":  "Authorization: [скрыто]",
		"curl -H 'x' bearer abcdef123456":     "curl -H 'x' Bearer [скрыто]",
		"ключ ghp_" + strings.Repeat("a", 24): "ключ [скрыто]",
		"/Users/alex/work/x":                  "~/work/x",
		"обычный текст без секретов":          "обычный текст без секретов",
	} {
		if got := mask.text(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}
