package deepv2

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// ErrEmptyGroupKeyComponent is returned by GroupKey when a component is empty after
// normalization.
var ErrEmptyGroupKeyComponent = errors.New("group key components must be nonempty")

// GroupKey is the stable key of a reviewed change intent, independent of the session:
// proposal_group_key of v2_contract.py. Each component is NFKC-normalized, case-folded and
// has its whitespace collapsed; the key is "p2:" and the first 20 hex digits of the SHA-256
// of the components encoded as Python json.dumps(ensure_ascii=False, separators=(",", ":")).
func GroupKey(patternID, scope, changeIntent string) (string, error) {
	components := []string{normalize(patternID), normalize(scope), normalize(changeIntent)}
	for _, c := range components {
		if c == "" {
			return "", ErrEmptyGroupKeyComponent
		}
	}
	sum := sha256.Sum256([]byte(pythonJSONStrings(components)))
	return "p2:" + hex.EncodeToString(sum[:])[:20], nil
}

// normalize is Python " ".join(unicodedata.normalize("NFKC", v).casefold().split()).
func normalize(value string) string {
	folded := cases.Fold().String(norm.NFKC.String(value))
	return strings.Join(strings.FieldsFunc(folded, pythonSpace), " ")
}

// pythonSpace is Python str.isspace: Go's White_Space set plus the information separators
// U+001C..U+001F, which Python also splits on.
func pythonSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// pythonJSONStrings encodes a string array as Python json.dumps(ensure_ascii=False) does:
// only '"', '\\' and control characters below U+0020 are escaped. encoding/json would also
// escape <, >, & and U+2028/U+2029 and so change the key.
func pythonJSONStrings(values []string) string {
	const hexDigits = "0123456789abcdef"
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for _, r := range v {
			switch r {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			default:
				if r < 0x20 {
					b.WriteString(`\u00`)
					b.WriteByte(hexDigits[r>>4])
					b.WriteByte(hexDigits[r&0xf])
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}
