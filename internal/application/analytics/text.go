package analytics

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// Redact masks things that look like secrets: private keys, URL userinfo, key=value
// secrets, secret CLI flags, bearer tokens, JWT, vendor tokens and long blobs. The rules are
// pkg/redact's, shared with the coach journal and the decision journal; analytics adds the
// long-blob guess, because every signed-in user sees every user's texts here.
func Redact(text string) string { return redact.SecretsAndBlobs(text) }

// Clean prepares a text for a response: it masks secrets, replaces /Users/<name> with ~,
// collapses whitespace and cuts the result to limit runes, the last one being "…".
// Masking runs on the whole text before the cut, so a cut never leaves a part of a
// secret that its rule would no longer recognise. A limit below one keeps the first rune
// and "…", or "…" alone for an empty text, as the colleague's clean() does.
func Clean(text string, limit int) string { return cleanWith(nil, text, limit) }

// redactWith is Redact through the build's memo; a nil memo masks every time.
func redactWith(memo *redact.Memo, text string) string { return memo.SecretsAndBlobs(text) }

// cleanWith is Clean through the build's memo; a nil memo masks every time.
func cleanWith(memo *redact.Memo, text string, limit int) string {
	text = redact.Home(redactWith(memo, text))
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	keep := min(len(runes), max(1, limit-1))
	return strings.TrimRightFunc(string(runes[:keep]), unicode.IsSpace) + "…"
}

// ShortID shortens a session id to its first 8 and last 4 characters. The first 8
// characters of a UUIDv7 are its time in steps of about 65 s, so sessions started in
// the same minute differ only by the tail.
func ShortID(sid string) string {
	runes := []rune(sid)
	if len(runes) <= 12 {
		return sid
	}
	return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
}

// FmtKtok formats a token count: below 1000 as is, otherwise in thousands rounded half
// to even and grouped by spaces ("1 235 тыс."); an unknown count is "—".
func FmtKtok(n *int64) string {
	if n == nil {
		return "—"
	}
	if *n < 1000 {
		return strconv.FormatInt(*n, 10)
	}
	return groupThousands(int64(math.RoundToEven(float64(*n)/1000))) + " тыс."
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// FmtDur formats a duration in seconds: seconds below 90 s, minutes below 90 min,
// hours above, with one decimal.
func FmtDur(seconds float64) string {
	switch {
	case seconds < 90:
		return strconv.FormatFloat(seconds, 'f', 1, 64) + " с"
	case seconds < 5400:
		return strconv.FormatFloat(seconds/60, 'f', 1, 64) + " мин"
	default:
		return strconv.FormatFloat(seconds/3600, 'f', 1, 64) + " ч"
	}
}

// FmtUSD formats dollars: cents from one cent up, four decimals below.
func FmtUSD(v float64) string {
	if v >= 0.01 {
		return "$" + strconv.FormatFloat(v, 'f', 2, 64)
	}
	return "$" + strconv.FormatFloat(v, 'f', 4, 64)
}
