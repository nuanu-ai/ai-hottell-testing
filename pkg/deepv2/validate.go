package deepv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ValidationError is a contract violation at a field path, as _fail of v2_contract.py raises
// it: the agent sees "deep.tasks[0].claims[1].line: completion claim outside task boundary"
// and repairs that field.
type ValidationError struct {
	Path   string
	Reason string
}

func (e *ValidationError) Error() string { return e.Path + ": " + e.Reason }

func fail(path, reason string) error { return &ValidationError{Path: path, Reason: reason} }

var (
	lineRef  = regexp.MustCompile(`^L([1-9][0-9]*)$`) //nolint:gochecknoglobals // compiled once, never written
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)   //nolint:gochecknoglobals // compiled once, never written
)

// Decode parses one JSON document the way the validators expect it: numbers stay
// json.Number, so a line written as 1.0 is told apart from 1, as Python's json does.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode report: data after the JSON document")
	}
	return value, nil
}

// object is _object: value must be a JSON object holding every required key and no key
// outside required and optional.
func object(value any, path string, required, optional []string) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fail(path, "expected object")
	}
	var missing, extra []string
	for _, key := range required {
		if _, ok := m[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range m {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			extra = append(extra, key)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return nil, fail(path, fmt.Sprintf("missing %s, unexpected %s", pyList(missing), pyList(extra)))
	}
	return m, nil
}

// str is _string: value must be a string, and with nonempty one that is not only whitespace.
func str(value any, path string, nonempty bool) (string, error) {
	s, ok := value.(string)
	if !ok || (nonempty && strings.TrimFunc(s, pythonSpace) == "") {
		if nonempty {
			return "", fail(path, "expected nonempty string")
		}
		return "", fail(path, "expected string")
	}
	return s, nil
}

// strs is _strings: value must be an array of nonempty strings, and with nonempty not empty.
func strs(value any, path string, nonempty bool) ([]string, error) {
	items, ok := value.([]any)
	if !ok || (nonempty && len(items) == 0) {
		return nil, fail(path, "expected string array")
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, err := str(item, fmt.Sprintf("%s[%d]", path, i), true)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// choice is _choice: value must be one of allowed.
func choice(value any, path string, allowed []string) (string, error) {
	s, ok := value.(string)
	if !ok || !slices.Contains(allowed, s) {
		return "", fail(path, "must be one of "+pyList(allowed))
	}
	return s, nil
}

// intValue reads a JSON integer: a json.Number written without fraction or exponent, or a Go
// int. Like Python's type(value) is int, a float, a bool or 1.0 is not one.
func intValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			return 0, false
		}
		n, err := strconv.Atoi(string(v))
		return n, err == nil
	default:
		return 0, false
	}
}

// positiveLine is _positive_line: a line number inside the frozen source 1..sourceRecords.
func positiveLine(value any, path string, sourceRecords int) (int, error) {
	n, ok := intValue(value)
	if !ok || n < 1 || n > sourceRecords {
		return 0, fail(path, fmt.Sprintf("line outside frozen source 1..%d", sourceRecords))
	}
	return n, nil
}

// evidence is _evidence: an array of L<n> references inside the frozen source; it returns the
// line numbers.
func evidence(value any, path string, sourceRecords int, nonempty bool) ([]int, error) {
	refs, err := strs(value, path, nonempty)
	if err != nil {
		return nil, err
	}
	lines := make([]int, 0, len(refs))
	for i, ref := range refs {
		at := fmt.Sprintf("%s[%d]", path, i)
		m := lineRef.FindStringSubmatch(ref)
		if m == nil {
			return nil, fail(at, "expected local source reference L<number>")
		}
		n, err := positiveLine(json.Number(m[1]), at, sourceRecords)
		if err != nil {
			return nil, err
		}
		lines = append(lines, n)
	}
	return lines, nil
}

func within(lines []int, start, end int) bool {
	for _, n := range lines {
		if n < start || n > end {
			return false
		}
	}
	return true
}

// pyList renders strings as Python's repr of a sorted list, as the reasons of v2_contract.py
// print them: ['a', 'b'].
func pyList(values []string) string {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	parts := make([]string, len(sorted))
	for i, v := range sorted {
		parts[i] = pyRepr(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// pyRepr is Python's repr of a str: single quotes unless the text holds a single quote and no
// double one, and escapes for the quote, the backslash and characters that are not printable.
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case !strconv.IsPrint(r):
			switch {
			case r < 0x100:
				fmt.Fprintf(&b, `\x%02x`, r)
			case r < 0x10000:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}
