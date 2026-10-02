package codex

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// The local MCP server hottell-local lives in config.toml as the table
// [mcp_servers.<name>]. Of that table hottell owns only command and args: the user's keys
// (enabled, tool_timeout_sec, …) and sub-tables such as [mcp_servers.<name>.env] stay. A
// definition outside the table (inline in [mcp_servers], a root-level dotted key or an
// array of tables), or mcp_servers itself written as a value or an array of tables, is the
// user's: install refuses it rather than write a table Codex can no longer parse, and
// uninstall leaves it.

// mcpLocalArgs is the args value of the hottell-local table.
const mcpLocalArgs = `["mcp-local"]`

// EnsureMCPLocal sets command and args of [mcp_servers.<name>] to start binaryPath
// mcp-local. An existing table is edited in place, only those two lines replaced or
// inserted below its header; a table is appended only when there is none. A file that
// already holds that is not written, and a missing config.toml is created.
func EnsureMCPLocal(codexHome, name, binaryPath string) error {
	path := ConfigFile(codexHome)
	return editConfig(path, func(data []byte) ([]byte, bool, error) {
		doc, err := parseServer(data, name)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		if doc.outside {
			return nil, false, fmt.Errorf("%s: %w: mcp_servers.%s is defined outside its own table", path, ErrMalformed, name)
		}
		out := doc.ensure(name, quote(binaryPath))
		return out, !bytes.Equal(out, data), nil
	})
}

// RemoveMCPLocal takes [mcp_servers.<name>] and its sub-tables out of config.toml, with the
// blank line above them; a missing file or table is left as it is, and so is a definition
// outside the table, which is the user's.
func RemoveMCPLocal(codexHome, name string) error {
	path := ConfigFile(codexHome)
	return editConfig(path, func(data []byte) ([]byte, bool, error) {
		doc, err := parseServer(data, name)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		rest, own := doc.cut()
		return trimEnd(rest), own != "", nil
	})
}

// serverDoc is config.toml split into statements, with the table of one MCP server.
type serverDoc struct {
	stmts []statement
	// owned marks the statements of [mcp_servers.<name>] and its sub-tables, with the
	// comments and blank lines between them.
	owned []bool
	// header is the index of the [mcp_servers.<name>] header, and command and args those
	// of its pairs; -1 when missing.
	header, command, args int
	// outside: mcp_servers.<name> or mcp_servers itself is also written outside those
	// tables.
	outside bool
}

// parseServer scans config.toml for [mcp_servers.<name>] and its sub-tables. A value
// written at mcp_servers or under mcp_servers.<name> from outside them, or an array of
// tables at either, sets outside, the way mark does for hooks.
func parseServer(data []byte, name string) (*serverDoc, error) {
	stmts, err := scan(string(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	ours := func(path []string) bool { return len(path) >= 2 && path[0] == "mcp_servers" && path[1] == name }
	bare := func(path []string) bool { return len(path) == 1 && path[0] == "mcp_servers" }
	doc := &serverDoc{stmts: stmts, owned: make([]bool, len(stmts)), header: -1, command: -1, args: -1}
	var table []string
	inOwn := false
	for i, s := range stmts {
		switch s.kind {
		case header:
			table = s.path
			inOwn = !s.array && ours(s.path)
			if s.array && (ours(s.path) || bare(s.path)) {
				doc.outside = true
			}
			doc.owned[i] = inOwn
			if inOwn && len(s.path) == 2 && doc.header < 0 {
				doc.header = i
			}
		case pair:
			if inOwn {
				doc.owned[i] = true
				doc.note(i, table)
				continue
			}
			if full := slices.Concat(table, s.path); ours(full) || bare(full) {
				doc.outside = true
			}
		case trivia:
		}
	}
	for i, s := range stmts {
		if s.kind == trivia {
			doc.owned[i] = ownedNeighbour(stmts, doc.owned, i, -1) && ownedNeighbour(stmts, doc.owned, i, 1)
		}
	}
	return doc, nil
}

// note remembers the pair at i when it is command or args of the [mcp_servers.<name>]
// table itself, not of a sub-table.
func (d *serverDoc) note(i int, table []string) {
	if len(table) != 2 || d.header < 0 {
		return
	}
	switch {
	case slices.Equal(d.stmts[i].path, []string{"command"}) && d.command < 0:
		d.command = i
	case slices.Equal(d.stmts[i].path, []string{"args"}) && d.args < 0:
		d.args = i
	}
}

// ensure returns the file with command and args of [mcp_servers.<name>] set to command and
// mcpLocalArgs, following the replace and insert of trustDoc.edit.
func (d *serverDoc) ensure(name, command string) []byte {
	commandLine := "command = " + command + "\n"
	argsLine := "args = " + mcpLocalArgs + "\n"
	if d.header < 0 {
		return appendBlock([]byte(d.text()), "[mcp_servers."+name+"]\n"+commandLine+argsLine)
	}
	replace := map[int]string{}
	insertAfter := map[int]string{}
	add := func(after int, line string) {
		if !strings.HasSuffix(d.stmts[after].text, "\n") && insertAfter[after] == "" {
			line = "\n" + line
		}
		insertAfter[after] += line
	}
	if d.command >= 0 {
		replace[d.command] = withPairValue(d.stmts[d.command].text, command)
	} else {
		add(d.header, commandLine)
	}
	switch {
	case d.args >= 0:
		replace[d.args] = withPairValue(d.stmts[d.args].text, mcpLocalArgs)
	case d.command >= 0:
		add(d.command, argsLine)
	default:
		add(d.header, argsLine)
	}
	var b strings.Builder
	for i, s := range d.stmts {
		if text, ok := replace[i]; ok {
			b.WriteString(text)
		} else {
			b.WriteString(s.text)
		}
		b.WriteString(insertAfter[i])
	}
	return []byte(b.String())
}

// cut splits the file into [mcp_servers.<name>] with its sub-tables and the rest, keeping
// a comment or blank line with the table only between two of its statements.
func (d *serverDoc) cut() (rest []byte, table string) {
	var kept, cut strings.Builder
	removed := false
	for i, s := range d.stmts {
		if d.owned[i] {
			cut.WriteString(s.text)
			removed = true
			continue
		}
		// Removing a table between two blank lines would leave two in a row.
		if removed && isBlank(s) && strings.HasSuffix(kept.String(), "\n\n") {
			removed = false
			continue
		}
		removed = false
		kept.WriteString(s.text)
	}
	return []byte(kept.String()), cut.String()
}

// text is the file as it was read.
func (d *serverDoc) text() string {
	var b strings.Builder
	for _, s := range d.stmts {
		b.WriteString(s.text)
	}
	return b.String()
}

// withPairValue replaces the value of a key = value statement with value, keeping the key,
// the comment after the value and the line end.
func withPairValue(text, value string) string {
	start, end, ok := pairValueSpan(text)
	if !ok {
		key := strings.TrimSpace(text[:max(strings.IndexByte(text, '='), 0)])
		line := key + " = " + value
		if strings.HasSuffix(text, "\n") {
			line += "\n"
		}
		return line
	}
	return text[:start] + value + text[end:]
}

// pairValueSpan finds the value of a key = value statement, which may span lines, without
// the spaces and the comment after it.
func pairValueSpan(text string) (start, end int, ok bool) {
	_, eq, err := scanKey(text, skipSpace(text, 0), '=')
	if err != nil {
		return 0, 0, false
	}
	start = skipSpace(text, eq+1)
	end = start
	depth := 0
	for i := start; i < len(text); {
		switch c := text[i]; c {
		case '"', '\'':
			j, err := scanString(text, i)
			if err != nil {
				return 0, 0, false
			}
			i, end = j, j
		case '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case '\n':
			if depth <= 0 {
				return start, end, end > start
			}
			i++
		case ' ', '\t', '\r':
			i++
		case '[', '{':
			depth++
			i++
			end = i
		case ']', '}':
			depth--
			i++
			end = i
		default:
			i++
			end = i
		}
	}
	return start, end, end > start
}
