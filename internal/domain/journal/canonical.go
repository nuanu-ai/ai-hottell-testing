package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"unicode/utf8"
)

// integerRe is a JSON integer as Python's json writes an int: no fraction, no exponent, no
// leading zero, no plus sign.
var integerRe = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`) //nolint:gochecknoglobals // compiled once, never written

// maxExactFloat is the largest float64 below which every integer is exact.
const maxExactFloat = 1 << 53

// Canonical encodes v byte for byte as Python's json.dumps(v, sort_keys=True,
// ensure_ascii=False, separators=(",", ":")).encode("utf-8"): keys sorted by code point, no
// spaces, non-ASCII as is, and only '"', '\\' and control characters escaped. encoding/json
// would also escape <, >, & and U+2028/U+2029 even with SetEscapeHTML(false).
//
// v is a JSON value as decoding gives it: map[string]any, []any, []string, string, bool,
// nil, json.Number, int, int64 or float64. Numbers are integers only, as everywhere in the
// journal: a fraction, an exponent or 2.0 written as a json.Number is an error. A float64
// is accepted when it holds an exact integer, because a decoder without UseNumber turns 2
// into one. Invalid UTF-8 is an error.
func Canonical(v any) ([]byte, error) {
	var b []byte
	b, err := appendValue(b, v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// SHA256Hex is the lowercase hex sha256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func appendValue(b []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(b, "null"...), nil
	case bool:
		return strconv.AppendBool(b, x), nil
	case string:
		return appendString(b, x)
	case int:
		return strconv.AppendInt(b, int64(x), 10), nil
	case int64:
		return strconv.AppendInt(b, x, 10), nil
	case json.Number:
		if !integerRe.MatchString(string(x)) {
			return nil, fmt.Errorf("canonical: %q is not an integer", string(x))
		}
		if x == "-0" {
			return append(b, '0'), nil
		}
		return append(b, x...), nil
	case float64:
		if x != math.Trunc(x) || math.Abs(x) >= maxExactFloat {
			return nil, fmt.Errorf("canonical: %v is not an exact integer", x)
		}
		return strconv.AppendInt(b, int64(x), 10), nil
	case []string:
		b = append(b, '[')
		for i, s := range x {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			if b, err = appendString(b, s); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case []any:
		b = append(b, '[')
		for i, e := range x {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			if b, err = appendValue(b, e); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys) // byte order of UTF-8 is code point order
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			if b, err = appendString(b, k); err != nil {
				return nil, err
			}
			b = append(b, ':')
			if b, err = appendValue(b, x[k]); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	default:
		return nil, fmt.Errorf("canonical: unsupported type %T", v)
	}
}

func appendString(b []byte, s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, errors.New("canonical: invalid UTF-8")
	}
	const hexDigits = "0123456789abcdef"
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, `\"`...)
		case '\\':
			b = append(b, `\\`...)
		case '\n':
			b = append(b, `\n`...)
		case '\r':
			b = append(b, `\r`...)
		case '\t':
			b = append(b, `\t`...)
		case '\b':
			b = append(b, `\b`...)
		case '\f':
			b = append(b, `\f`...)
		default:
			if r < 0x20 {
				b = append(b, `\u00`...)
				b = append(b, hexDigits[r>>4], hexDigits[r&0xf])
			} else {
				b = utf8.AppendRune(b, r)
			}
		}
	}
	return append(b, '"'), nil
}
