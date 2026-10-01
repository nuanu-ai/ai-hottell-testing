package codex

import (
	"errors"
	"fmt"
	"strings"
)

// statement is one line-level piece of a TOML file: a table header, a key/value pair
// (a multi-line value spans several lines), or a blank or comment line. Text ends with
// its newline, except at the end of a file without one.
type statement struct {
	text string
	kind statementKind
	// key is the first segment of the header's or the pair's key, unquoted.
	key string
	// path is every segment of that key, unquoted.
	path []string
	// array marks an [[array of tables]] header.
	array bool
}

type statementKind int

const (
	trivia statementKind = iota
	header
	pair
)

// family is the [otel] family cut out of a file.
type family struct {
	// Root holds the root-level pairs whose key starts with otel: otel.x = … and
	// otel = { … }. They are only valid before the first table header.
	Root string
	// Tables holds the [otel] and [otel.…] tables with the lines between them.
	Tables string
}

func (f family) empty() bool { return f.Root == "" && f.Tables == "" }

// text is the family as one string, used to recognise hottell's own.
func (f family) text() string { return f.Root + f.Tables }

// cutOTel splits a config.toml into the family of the otel key and the rest of the
// file. A comment or blank line goes with the family only between two of its
// statements, so a comment above a user's table stays with that table.
func cutOTel(data []byte) (rest []byte, fam family, err error) {
	stmts, err := scan(string(data))
	if err != nil {
		return nil, family{}, err
	}

	owned := make([]bool, len(stmts))
	inRoot, inOTel := true, false
	for i, s := range stmts {
		switch s.kind {
		case header:
			inRoot = false
			inOTel = s.key == "otel"
			owned[i] = inOTel
		case pair:
			owned[i] = inOTel || (inRoot && s.key == "otel")
		case trivia:
		}
	}
	// Trivia belongs to the family only between two of its statements.
	for i, s := range stmts {
		if s.kind != trivia {
			continue
		}
		owned[i] = ownedNeighbour(stmts, owned, i, -1) && ownedNeighbour(stmts, owned, i, 1)
	}
	// So do the comment lines right above one of its table headers.
	for i, s := range stmts {
		if s.kind != header || !owned[i] {
			continue
		}
		for j := i - 1; j >= 0 && stmts[j].kind == trivia && !isBlank(stmts[j]); j-- {
			owned[j] = true
		}
	}

	var kept, root, tables strings.Builder
	cut := false
	for i, s := range stmts {
		if owned[i] {
			switch {
			case isRootPair(stmts, i) && s.kind != header && !commentAboveHeader(stmts, i):
				root.WriteString(s.text)
			default:
				// Tables cut from separate places are kept apart by a blank line.
				if !cut && tables.Len() > 0 && !strings.HasSuffix(tables.String(), "\n\n") {
					tables.WriteString("\n")
				}
				tables.WriteString(s.text)
			}
			cut = true
			continue
		}
		// Removing a family between two blank lines would leave two in a row.
		if cut && isBlank(s) && strings.HasSuffix(kept.String(), "\n\n") {
			cut = false
			continue
		}
		cut = false
		kept.WriteString(s.text)
	}
	return []byte(kept.String()), family{Root: root.String(), Tables: tables.String()}, nil
}

// isRootPair reports whether the statement at i comes before the first table header.
func isRootPair(stmts []statement, i int) bool {
	for _, s := range stmts[:i] {
		if s.kind == header {
			return false
		}
	}
	return true
}

// commentAboveHeader reports whether the statement at i is in the run of comment lines
// directly above a table header.
func commentAboveHeader(stmts []statement, i int) bool {
	for j := i; j < len(stmts); j++ {
		switch {
		case stmts[j].kind == header:
			return true
		case stmts[j].kind != trivia || isBlank(stmts[j]):
			return false
		}
	}
	return false
}

// ownedNeighbour reports whether the nearest non-trivia statement from i in direction
// step is owned; there is none past either end of the file.
func ownedNeighbour(stmts []statement, owned []bool, i, step int) bool {
	for j := i + step; j >= 0 && j < len(stmts); j += step {
		if stmts[j].kind != trivia {
			return owned[j]
		}
	}
	return false
}

func isBlank(s statement) bool { return s.kind == trivia && strings.TrimSpace(s.text) == "" }

// insertRoot puts root-level pairs at the end of the file's root, before its first
// table header.
func insertRoot(data []byte, text string) ([]byte, error) {
	if text == "" {
		return data, nil
	}
	stmts, err := scan(string(data))
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	inserted := false
	for _, s := range stmts {
		if !inserted && s.kind == header {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
			b.WriteString(text)
			if !strings.HasSuffix(text, "\n") {
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
			inserted = true
		}
		b.WriteString(s.text)
	}
	if !inserted {
		out := strings.TrimRight(b.String(), " \t\r\n")
		if out != "" {
			out += "\n"
		}
		return []byte(out + strings.TrimRight(text, " \t\r\n") + "\n"), nil
	}
	return []byte(b.String()), nil
}

// scan splits TOML text into statements. It knows only as much TOML as it takes to
// find where a statement ends: strings of all four kinds, comments, and arrays and
// inline tables that span lines.
func scan(src string) ([]statement, error) {
	var stmts []statement
	for pos := 0; pos < len(src); {
		start := pos
		i := skipSpace(src, pos)
		switch {
		case i >= len(src) || src[i] == '\n' || src[i] == '\r' || src[i] == '#':
			pos = lineEnd(src, i)
			stmts = append(stmts, statement{text: src[start:pos], kind: trivia})
		case src[i] == '[':
			path, array, end, err := scanHeader(src, i)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, statement{text: src[start:end], kind: header, key: path[0], path: path, array: array})
			pos = end
		default:
			path, afterKey, err := scanKey(src, i, '=')
			if err != nil {
				return nil, err
			}
			end, err := scanValue(src, afterKey+1)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, statement{text: src[start:end], kind: pair, key: path[0], path: path})
			pos = end
		}
	}
	return stmts, nil
}

// scanHeader reads [a.b] or [[a.b]] at i and returns the key segments, whether it is an
// array of tables and the end of the line.
func scanHeader(src string, i int) ([]string, bool, int, error) {
	i++
	closing := "]"
	if i < len(src) && src[i] == '[' {
		i++
		closing = "]]"
	}
	path, end, err := scanKey(src, i, ']')
	if err != nil {
		return nil, false, 0, err
	}
	if !strings.HasPrefix(src[end:], closing) {
		return nil, false, 0, fmt.Errorf("unterminated table header at byte %d", i)
	}
	end, err = restOfLine(src, end+len(closing))
	return path, closing == "]]", end, err
}

// scanKey reads a dotted key at i up to stop and returns its segments unquoted and the
// position of stop. Of the escapes of a basic string only \\ and \" are decoded, which
// is all a Codex hook path needs.
func scanKey(src string, i int, stop byte) ([]string, int, error) {
	var path []string
	for {
		i = skipSpace(src, i)
		if i >= len(src) {
			return nil, 0, errors.New("unexpected end of file in a key")
		}
		var part string
		switch c := src[i]; c {
		case '"', '\'':
			end, err := scanString(src, i)
			if err != nil {
				return nil, 0, err
			}
			part = src[i+1 : end-1]
			if c == '"' {
				part = strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(part)
			}
			i = end
		default:
			j := i
			for j < len(src) && isBareKeyChar(src[j]) {
				j++
			}
			if j == i {
				return nil, 0, fmt.Errorf("unexpected %q in a key at byte %d", c, i)
			}
			part = src[i:j]
			i = j
		}
		path = append(path, part)
		i = skipSpace(src, i)
		if i >= len(src) {
			return nil, 0, errors.New("unexpected end of file in a key")
		}
		switch src[i] {
		case '.':
			i++
		case stop:
			return path, i, nil
		default:
			return nil, 0, fmt.Errorf("unexpected %q after a key at byte %d", src[i], i)
		}
	}
}

// scanValue reads a value starting at i and returns the end of its last line.
func scanValue(src string, i int) (int, error) {
	depth := 0
	for i < len(src) {
		switch c := src[i]; c {
		case '"', '\'':
			end, err := scanString(src, i)
			if err != nil {
				return 0, err
			}
			i = end
		case '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case '[', '{':
			depth++
			i++
		case ']', '}':
			depth--
			i++
		case '\n':
			if depth <= 0 {
				return i + 1, nil
			}
			i++
		default:
			i++
		}
	}
	if depth > 0 {
		return 0, errors.New("unexpected end of file in an array or inline table")
	}
	return len(src), nil
}

// scanString reads a string of any kind starting at its opening quote and returns the
// position after its closing one.
func scanString(src string, i int) (int, error) {
	q := src[i]
	if strings.HasPrefix(src[i:], strings.Repeat(string(q), 3)) {
		delim := strings.Repeat(string(q), 3)
		j := i + 3
		for {
			k := strings.Index(src[j:], delim)
			if k < 0 {
				return 0, fmt.Errorf("unterminated multi-line string at byte %d", i)
			}
			j += k
			if q == '"' && escaped(src, i+3, j) {
				j++
				continue
			}
			// Up to two quotes may directly precede the closing delimiter.
			end := j + 3
			for end < len(src) && src[end] == q && end-j < 5 {
				end++
			}
			return end, nil
		}
	}
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			if q == '"' {
				j++
			}
		case q:
			return j + 1, nil
		case '\n':
			return 0, fmt.Errorf("unterminated string at byte %d", i)
		}
	}
	return 0, fmt.Errorf("unterminated string at byte %d", i)
}

// escaped reports whether the byte at j is escaped by an odd run of backslashes that
// starts at or after from.
func escaped(src string, from, j int) bool {
	n := 0
	for k := j - 1; k >= from && src[k] == '\\'; k-- {
		n++
	}
	return n%2 == 1
}

// restOfLine returns the end of the line after a header: only a comment may follow.
func restOfLine(src string, i int) (int, error) {
	i = skipSpace(src, i)
	if i < len(src) && src[i] != '#' && src[i] != '\n' && src[i] != '\r' {
		return 0, fmt.Errorf("unexpected %q after a table header at byte %d", src[i], i)
	}
	return lineEnd(src, i), nil
}

// lineEnd returns the position after the newline that ends the line holding i.
func lineEnd(src string, i int) int {
	if k := strings.IndexByte(src[i:], '\n'); k >= 0 {
		return i + k + 1
	}
	return len(src)
}

func skipSpace(src string, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return i
}

func isBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}
