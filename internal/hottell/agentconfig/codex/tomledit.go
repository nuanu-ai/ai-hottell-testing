package codex

import (
	"fmt"
	"slices"
	"strings"
)

// trustTable is a [hooks.state."<key>"] table of config.toml: the indexes of its header
// and its trusted_hash pair among the statements, and how many pairs it holds.
type trustTable struct {
	header int
	hash   int
	pairs  int
}

// trustDoc is config.toml split into statements, with the tables that keep hook trust.
type trustDoc struct {
	stmts  []statement
	tables map[string]*trustTable
	// irregular holds the keys whose trust is also written outside their own table,
	// such as a dotted key or an inline table; all means hooks or hooks.state itself is.
	irregular map[string]bool
	all       bool
}

// parseTrust scans config.toml for the [hooks.state."<key>"] tables.
func parseTrust(data []byte) (*trustDoc, error) {
	stmts, err := scan(string(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	doc := &trustDoc{stmts: stmts, tables: map[string]*trustTable{}, irregular: map[string]bool{}}
	var table []string
	var current *trustTable
	for i, s := range stmts {
		switch s.kind {
		case header:
			table, current = s.path, nil
			if key, ok := stateKey(s.path); ok && !s.array {
				current = &trustTable{header: i, hash: -1}
				doc.tables[key] = current
			} else if s.array {
				doc.mark(s.path)
			}
		case pair:
			if current != nil {
				current.pairs++
				if slices.Equal(s.path, []string{trustedHashKey}) {
					current.hash = i
				}
				continue
			}
			doc.mark(slices.Concat(table, s.path))
		default:
		}
	}
	return doc, nil
}

// mark records a value written at path outside a [hooks.state."<key>"] table.
func (d *trustDoc) mark(path []string) {
	switch {
	case len(path) == 0 || path[0] != "hooks":
	case len(path) == 1 || (len(path) == 2 && path[1] == "state"):
		d.all = true
	case path[1] == "state":
		d.irregular[path[2]] = true
	}
}

// stateKey returns <key> of a hooks.state.<key> path.
func stateKey(path []string) (string, bool) {
	if len(path) == 3 && path[0] == "hooks" && path[1] == "state" {
		return path[2], true
	}
	return "", false
}

// entries returns the trusted_hash of every [hooks.state."<key>"] table.
func (d *trustDoc) entries() map[string]trustEntry {
	out := make(map[string]trustEntry, len(d.tables))
	for key, t := range d.tables {
		entry := trustEntry{}
		if t.hash >= 0 {
			entry.hash, entry.hasHash = stringValue(d.stmts[t.hash].text)
		}
		out[key] = entry
	}
	return out
}

// edit sets the trusted_hash of the tables in set and removes it from those in remove,
// changing only those lines: a missing table is appended at the end of the file, and a
// table left with nothing in it goes with the blank line above it. Trust of these keys
// written in any other form is not edited.
func (d *trustDoc) edit(set map[string]string, remove []string) ([]byte, error) {
	for _, key := range slices.Concat(sortedKeys(set), remove) {
		if d.all || d.irregular[key] {
			return nil, fmt.Errorf("%w: trust of %q", ErrLayout, key)
		}
	}

	replace := map[int]string{}
	insertAfter := map[int]string{}
	drop := map[int]bool{}
	var appended strings.Builder
	for _, key := range sortedKeys(set) {
		line := trustedHashKey + " = " + tomlQuote(set[key])
		t, ok := d.tables[key]
		switch {
		case !ok:
			fmt.Fprintf(&appended, "\n[hooks.state.%s]\n%s\n", tomlQuote(key), line)
		case t.hash >= 0:
			replace[t.hash] = withValue(d.stmts[t.hash].text, tomlQuote(set[key]))
		case strings.HasSuffix(d.stmts[t.header].text, "\n"):
			insertAfter[t.header] = line + "\n"
		default:
			insertAfter[t.header] = "\n" + line
		}
	}
	for _, key := range remove {
		t, ok := d.tables[key]
		if !ok || t.hash < 0 {
			continue
		}
		drop[t.hash] = true
		if t.pairs == 1 {
			drop[t.header] = true
			if t.header > 0 && isBlank(d.stmts[t.header-1]) {
				drop[t.header-1] = true
			}
		}
	}

	var b strings.Builder
	for i, s := range d.stmts {
		switch {
		case drop[i]:
		case replace[i] != "":
			b.WriteString(replace[i])
		default:
			b.WriteString(s.text)
		}
		b.WriteString(insertAfter[i])
	}
	out := b.String()
	if appended.Len() > 0 {
		block := appended.String()
		switch {
		case strings.TrimSpace(out) == "":
			out, block = "", strings.TrimPrefix(block, "\n")
		case !strings.HasSuffix(out, "\n"):
			out += "\n"
		}
		out += block
	}
	return []byte(out), nil
}

// stringValue returns the value of a key = "string" statement.
func stringValue(text string) (string, bool) {
	start, end, ok := valueSpan(text)
	if !ok {
		return "", false
	}
	return text[start+1 : end-1], true
}

// withValue replaces the string value of a key = "string" statement with quoted, keeping
// the comment after it; a statement of another shape becomes a plain trusted_hash line.
func withValue(text, quoted string) string {
	start, end, ok := valueSpan(text)
	if !ok {
		return trustedHashKey + " = " + quoted + "\n"
	}
	return text[:start] + quoted + text[end:]
}

// valueSpan finds a single-line string value after the = of a statement.
func valueSpan(text string) (start, end int, ok bool) {
	eq := strings.IndexByte(text, '=')
	if eq < 0 {
		return 0, 0, false
	}
	start = skipSpace(text, eq+1)
	if start >= len(text) || (text[start] != '"' && text[start] != '\'') || strings.HasPrefix(text[start:], `"""`) || strings.HasPrefix(text[start:], "'''") {
		return 0, 0, false
	}
	end, err := scanString(text, start)
	if err != nil {
		return 0, 0, false
	}
	return start, end, true
}

// tomlQuote writes s as a TOML basic string.
func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
