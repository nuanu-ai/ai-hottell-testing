package sessions

import (
	"fmt"
	"unicode/utf8"
)

// Truncate cuts s to at most limit bytes, never inside a UTF-8 character, and says how long
// it was; limit <= 0 keeps s whole. The cut backs off at most the three continuation bytes
// a character can have: bytes that are not UTF-8 are cut at limit.
func Truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for back := 0; back < utf8.UTFMax-1 && cut > 0 && !utf8.RuneStart(s[cut]); back++ {
		cut--
	}
	if !utf8.RuneStart(s[cut]) {
		cut = limit
	}
	return s[:cut] + fmt.Sprintf("…[обрезано, всего %d байт]", len(s))
}
