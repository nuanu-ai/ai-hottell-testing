// Package codextrust computes the trust state Codex keeps for a hook in config.toml:
// the key of its [hooks.state."<key>"] table and the trusted_hash that marks it trusted,
// as codex-rs 0.159 derives them in hooks/src/engine/discovery.rs.
package codextrust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Codex hook events as hooks.json names them.
const (
	PreToolUse        = "PreToolUse"
	PermissionRequest = "PermissionRequest"
	PostToolUse       = "PostToolUse"
	PreCompact        = "PreCompact"
	PostCompact       = "PostCompact"
	SessionStart      = "SessionStart"
	SessionEnd        = "SessionEnd"
	UserPromptSubmit  = "UserPromptSubmit"
	SubagentStart     = "SubagentStart"
	SubagentStop      = "SubagentStop"
	Stop              = "Stop"
	Interrupt         = "Interrupt"
)

const (
	defaultTimeoutSec           = 600
	sessionEndDefaultTimeoutSec = 1
	sessionEndMaxTimeoutSec     = 3
	defaultContextLimit         = 2500
)

// Handler is a command hook handler as written in hooks.json. Nil pointers are fields
// left out of the file.
type Handler struct {
	Command                string
	TimeoutSec             *uint64
	Async                  bool
	StatusMessage          *string
	AdditionalContextLimit *uint64
}

// Key returns the [hooks.state] key of the handler at handlerIndex in the matcher group
// at groupIndex of event: source is the path of hooks.json as Codex resolves it, and the
// indexes count from zero in file order, so a group inserted before ours moves our key.
func Key(source, event string, groupIndex, handlerIndex int) (string, error) {
	label, ok := eventKeyLabel(event)
	if !ok {
		return "", fmt.Errorf("unknown hook event %q", event)
	}
	return fmt.Sprintf("%s:%s:%d:%d", source, label, groupIndex, handlerIndex), nil
}

// Hash returns the trusted_hash Codex expects for handler in a group of event with
// matcher (nil when the group has none): "sha256:" and the hex SHA-256 of the canonical
// JSON of the normalized hook identity.
func Hash(event string, matcher *string, handler Handler) (string, error) {
	label, ok := eventKeyLabel(event)
	if !ok {
		return "", fmt.Errorf("unknown hook event %q", event)
	}
	if strings.TrimSpace(handler.Command) == "" {
		return "", fmt.Errorf("empty hook command")
	}

	fields := []field{
		{"async", strconv.FormatBool(handler.Async)},
		{"command", quote(handler.Command)},
		{"timeout", strconv.FormatUint(timeoutSec(event, handler.TimeoutSec), 10)},
		{"type", quote("command")},
	}
	if handler.StatusMessage != nil {
		fields = append(fields, field{"statusMessage", quote(*handler.StatusMessage)})
	}
	if limit := handler.AdditionalContextLimit; limit != nil && *limit != defaultContextLimit && emitsContext(event) {
		fields = append(fields, field{"additionalContextLimit", strconv.FormatUint(*limit, 10)})
	}

	identity := []field{
		{"event_name", quote(label)},
		{"hooks", "[" + object(fields) + "]"},
	}
	if matcher != nil && usesMatcher(event) {
		identity = append(identity, field{"matcher", quote(*matcher)})
	}

	sum := sha256.Sum256([]byte(object(identity)))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// timeoutSec mirrors normalize_command_hook: the timeout is always part of the identity.
func timeoutSec(event string, timeout *uint64) uint64 {
	if event == SessionEnd || event == Interrupt {
		if timeout == nil {
			return sessionEndDefaultTimeoutSec
		}
		return min(max(*timeout, 1), sessionEndMaxTimeoutSec)
	}
	if timeout == nil {
		return defaultTimeoutSec
	}
	return max(*timeout, 1)
}

// usesMatcher mirrors matcher_pattern_for_event: these events drop the matcher.
func usesMatcher(event string) bool {
	return event != UserPromptSubmit && event != Stop && event != Interrupt
}

// emitsContext lists the events whose additionalContextLimit Codex keeps.
func emitsContext(event string) bool {
	switch event {
	case PreToolUse, PostToolUse, SessionStart, UserPromptSubmit, SubagentStart:
		return true
	}
	return false
}

func eventKeyLabel(event string) (string, bool) {
	switch event {
	case PreToolUse:
		return "pre_tool_use", true
	case PermissionRequest:
		return "permission_request", true
	case PostToolUse:
		return "post_tool_use", true
	case PreCompact:
		return "pre_compact", true
	case PostCompact:
		return "post_compact", true
	case SessionStart:
		return "session_start", true
	case SessionEnd:
		return "session_end", true
	case UserPromptSubmit:
		return "user_prompt_submit", true
	case SubagentStart:
		return "subagent_start", true
	case SubagentStop:
		return "subagent_stop", true
	case Stop:
		return "stop", true
	case Interrupt:
		return "interrupt", true
	}
	return "", false
}

// field is one member of a JSON object with its value already encoded.
type field struct {
	name  string
	value string
}

// object writes fields as a compact JSON object with keys in byte order, as
// serde_json::to_vec writes Codex's canonical_json.
func object(fields []field) string {
	sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(quote(f.name))
		b.WriteByte(':')
		b.WriteString(f.value)
	}
	b.WriteByte('}')
	return b.String()
}

// quote escapes s as serde_json does: only the quote, the backslash and control
// characters, with short forms where JSON has them; everything else stays raw UTF-8.
// encoding/json differs on <, >, &, U+2028 and U+2029.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(s) {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
