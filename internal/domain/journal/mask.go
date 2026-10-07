package journal

import (
	"unicode/utf8"

	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// maxFieldRunes is how long a free text of a coach record may be after masking: longer is
// cut, the last character being "…", as hottell-local's coach journal does (HT-361).
const maxFieldRunes = 1000

// Mask hides secrets and the home directory in a free text of the journal and of the Deep
// reports, with the rules pkg/redact shares with analytics and the coach of hottell-local,
// without analytics' long-blob guess: a hash or an id in a journal text stays readable.
// record_hash and candidate_sha256 are computed over masked texts.
//
// One pass of the rules is not always a fixed point (admin:a:b@host leaves admin:[скрыто]@host,
// which a second pass masks again), and Seal refuses a text Mask would change, so Mask repeats
// the pass until the text holds, at most maskPasses times.
func Mask(s string) string {
	for range maskPasses {
		next := redact.Home(redact.Secrets(s))
		if next == s {
			break
		}
		s = next
	}
	return s
}

// maskPasses bounds the passes of Mask; a text still changing after them is refused by the
// guard of Seal rather than stored.
const maskPasses = 8

// cutMasked shortens a masked text s to limit runes, the last one being "…". It runs after
// masking, so it never leaves a part of a secret that its rule would no longer recognise. When the cut lands inside a mask
// or right after a keyword, so that Mask would change the cut text again, it cuts further
// back until Mask leaves the result as it is. The guard of Seal and ValidateCoach
// (Mask(text) == text) then accepts what MaskCoachEntry wrote.
func cutMasked(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	for n := limit - 1; n > 0; n-- {
		if out := string(r[:n]) + "…"; Mask(out) == out {
			return out
		}
	}
	return "…"
}
