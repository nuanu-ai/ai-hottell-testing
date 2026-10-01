package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// defaultIndent is what a new hooks.json is indented with.
const defaultIndent = "  "

// editHooks reads hooks.json in codexHome, lets change edit its hooks section and, when
// change reports a change, writes the file back atomically. A file that cannot be parsed
// is never written.
func editHooks(codexHome string, change func(hooks *object) (bool, error)) error {
	path := HooksFile(codexHome)
	data, err := readConfig(path)
	if err != nil {
		return err
	}

	doc := &object{}
	if len(bytes.TrimSpace(data)) > 0 {
		if !isKind(data, '{') {
			return fmt.Errorf("%s: %w: not a JSON object", path, ErrMalformed)
		}
		if doc, err = parseObject(data); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	hooks := &object{}
	if raw, found := doc.get("hooks"); found {
		if !isKind(raw, '{') {
			return fmt.Errorf("%s: %w: hooks is not an object", path, ErrMalformed)
		}
		if hooks, err = parseObject(raw); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	changed, err := change(hooks)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return nil
	}

	if len(hooks.members) == 0 {
		doc.remove("hooks")
	} else {
		encoded, err := hooks.encode()
		if err != nil {
			return err
		}
		doc.set("hooks", encoded)
	}
	out, err := formatJSON(doc, data)
	if err != nil {
		return err
	}
	return writeConfig(path, out)
}

// formatJSON indents doc the way the original file was indented, and ends it with a
// newline when the original did or there was none.
func formatJSON(doc *object, original []byte) ([]byte, error) {
	compact, err := doc.encode()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", indentOf(original)); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(original)) == 0 || bytes.HasSuffix(original, []byte("\n")) {
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// indentOf returns the whitespace that starts the first indented line of data.
func indentOf(data []byte) string {
	for line := range bytes.Lines(data) {
		trimmed := bytes.TrimLeft(line, " \t")
		if n := len(line) - len(trimmed); n > 0 && len(bytes.TrimSpace(trimmed)) > 0 {
			return string(line[:n])
		}
	}
	return defaultIndent
}
