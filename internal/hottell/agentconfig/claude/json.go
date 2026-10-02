package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// member is one key of a JSON object with its value as written in the file.
type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object that keeps the order of its keys and the text of the values it
// is not asked to change.
type object struct {
	members []member
}

func (o *object) keys() []string {
	keys := make([]string, len(o.members))
	for i, m := range o.members {
		keys[i] = m.key
	}
	return keys
}

func (o *object) get(key string) (json.RawMessage, bool) {
	for _, m := range o.members {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// set replaces the value of key in place, or appends the key when it is missing.
func (o *object) set(key string, value json.RawMessage) {
	for i, m := range o.members {
		if m.key == key {
			o.members[i].value = value
			return
		}
	}
	o.members = append(o.members, member{key: key, value: value})
}

func (o *object) remove(key string) {
	for i, m := range o.members {
		if m.key == key {
			o.members = append(o.members[:i], o.members[i+1:]...)
			return
		}
	}
}

// encode writes the object compactly, the values as they were.
func (o *object) encode() (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o.members {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := marshal(m.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// parseObject reads a JSON object, which must be the whole of data.
func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	o := &object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, malformed(err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("%w: object key %v", ErrMalformed, tok)
		}
		// Claude Code keeps the last of duplicate keys and this edit the first, so a file
		// with them is refused rather than edited to no effect.
		if _, dup := o.get(key); dup {
			return nil, fmt.Errorf("%w: duplicate key %q", ErrMalformed, key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, malformed(err)
		}
		o.members = append(o.members, member{key: key, value: value})
	}
	if err := expectDelim(dec, '}'); err != nil {
		return nil, err
	}
	return o, expectEnd(dec)
}

// parseArray reads a JSON array, which must be the whole of data.
func parseArray(data []byte) ([]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := expectDelim(dec, '['); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	for dec.More() {
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return nil, malformed(err)
		}
		items = append(items, item)
	}
	if err := expectDelim(dec, ']'); err != nil {
		return nil, err
	}
	return items, expectEnd(dec)
}

func encodeArray(items []json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// marshal encodes v without escaping <, > and &, which a shell command may hold.
func marshal(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return malformed(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("%w: want %s, got %v", ErrMalformed, want, tok)
	}
	return nil
}

func expectEnd(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: data after the end of the value", ErrMalformed)
	}
	return nil
}

func malformed(err error) error {
	return fmt.Errorf("%w: %w", ErrMalformed, err)
}
