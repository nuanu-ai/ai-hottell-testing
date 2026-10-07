package analytics

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The outcomes of a tool call.
const (
	// StateSuccess: the call succeeded.
	StateSuccess = "success"
	// StateError: the call failed.
	StateError = "error"
	// StateDone: the call finished, but nothing tells success from failure.
	StateDone = "done"
	// StateUnknown: no result of the call arrived.
	StateUnknown = "unknown"
)

// noteMax is the longest note of an outcome, in runes.
const noteMax = 160

// smallResponseMax is the longest output, in bytes, read as a JSON object for its outcome.
const smallResponseMax = 1500

// fromRollout ends a note taken from the facts of the session's rollout.
const fromRollout = " · из журнала сессии"

var (
	exitHeadRe    = regexp.MustCompile(`^(?:Exit code:\s*(-?\d+)|Chunk ID:[^\n]*\n(?:Wall time:[^\n]*\n)?Process exited with code\s*(-?\d+))`)
	isErrorTailRe = regexp.MustCompile(`"isError"\s*:\s*(true|false)\s*\}\s*$`)
	// errorNameRe and failureWordRe are the two tiers errorLine looks for, in order.
	errorNameRe   = regexp.MustCompile(`(?i)(\w*error\b|\bfatal\b|\w*exception\b|\btraceback\b)`)
	failureWordRe = regexp.MustCompile(`(?i)\b(failed|failure|not found|denied|no such|invalid|refused|timed? ?out)\b`)
)

// CallState is the outcome of a call and a note on it, as the colleague's call_state reads it,
// with our PostToolUseFailure first. In order: PostToolUseFailure is an error, noted by the hook's
// error text when it sent one; Claude's native
// tool_result decides; no Post is unknown; the facts of the Codex rollout (nil without them)
// decide by the exit code, then by a failed status; an exit code at the head of the output
// decides; "isError" at the tail decides; a small JSON output decides by isError, is_error,
// interrupted and error, and is a success with isError, accepted or timed_out; a Claude call is
// a success, Claude sending a Post only for a finished tool; a completed status in the rollout
// is a success. Otherwise the call is done. The note is at most 160 runes, through Clean.
func CallState(c *Call, facts *telemetry.ToolFact) (state, note string) {
	if c.Failed {
		if c.Failure != "" {
			return StateError, Clean(c.Failure, noteMax)
		}
		return StateError, "PostToolUseFailure"
	}
	if c.NativeSuccess != nil {
		if *c.NativeSuccess {
			return StateSuccess, ""
		}
		if c.NativeError != "" {
			return StateError, Clean(c.NativeError, noteMax)
		}
		return StateError, "tool_result: success=false"
	}
	if !c.HasPost() {
		return StateUnknown, "нет PostToolUse"
	}
	status := ""
	if facts != nil {
		status = strings.ToLower(strings.TrimSpace(facts.Status))
		if facts.ExitCode != nil {
			if *facts.ExitCode == 0 {
				return StateSuccess, ""
			}
			return StateError, "exit " + strconv.FormatInt(*facts.ExitCode, 10) + fromRollout
		}
		switch status {
		case "failed", "error", "declined", "rejected", "cancelled":
			return StateError, status + fromRollout
		}
	}
	if state, note, ok := outputState(c); ok {
		return state, note
	}
	if c.Agent == "claude" || status == "completed" {
		return StateSuccess, ""
	}
	return StateDone, ""
}

// outputState reads the outcome from the output's digest: an exit code at its head, "isError"
// at its tail, or a small JSON object.
func outputState(c *Call) (state, note string, ok bool) {
	head := strings.TrimLeft(c.RespHead, pyWhitespace)
	if m := exitHeadRe.FindStringSubmatch(head); m != nil {
		code := m[1]
		if code == "" {
			code = m[2]
		}
		n, _ := strconv.Atoi(code)
		if n == 0 {
			return StateSuccess, "", true
		}
		note := "exit " + strconv.Itoa(n)
		if _, detail, found := strings.Cut(head, "Output:"); found && strings.TrimSpace(detail) != "" {
			note += " · " + errorLine(detail, 110)
		}
		return StateError, Clean(note, noteMax), true
	}
	if m := isErrorTailRe.FindStringSubmatch(c.RespTail); m != nil {
		if m[1] == "false" {
			return StateSuccess, "", true
		}
		if line := firstLine(head, 120); line != "" {
			return StateError, Clean(line, noteMax), true
		}
		return StateError, "isError: true", true
	}
	if c.RespLen > smallResponseMax {
		return "", "", false
	}
	var small map[string]any
	if json.Unmarshal([]byte(head), &small) != nil || small == nil {
		return "", "", false
	}
	if small["isError"] == true || small["is_error"] == true {
		return StateError, Clean(pyDumps(head), noteMax), true
	}
	if small["interrupted"] == true {
		return StateError, "прервано", true
	}
	if err := small["error"]; isPyTruthyError(err) {
		text, isString := err.(string)
		if !isString {
			text = pyDumps(compactJSON(err))
		}
		return StateError, Clean("error: "+text, noteMax), true
	}
	_, hasIsError := small["isError"]
	_, hasTimedOut := small["timed_out"]
	if hasIsError || small["accepted"] == true || hasTimedOut || c.Agent == "claude" {
		return StateSuccess, "", true
	}
	return "", "", false
}

// pyWhitespace is what Python's str.lstrip() strips that matters in an output head.
const pyWhitespace = " \t\n\r\v\f"

// isPyTruthyError is the colleague's err not in (None, "", False, {}).
func isPyTruthyError(v any) bool {
	switch e := v.(type) {
	case nil:
		return false
	case string:
		return e != ""
	case bool:
		return e
	case map[string]any:
		return len(e) > 0
	}
	return true
}

// firstLine is the first non-blank line of text through Clean, "" when there is none.
func firstLine(text string, limit int) string {
	for line := range strings.Lines(text) {
		if strings.TrimSpace(line) != "" {
			return Clean(line, limit)
		}
	}
	return ""
}

// errorLine is the most telling line of an error output: a line naming an error, a fatal, an
// exception or a traceback, then one saying failed, denied, not found and the like, else the last
// non-blank line; through Clean.
func errorLine(text string, limit int) string {
	var lines []string
	for line := range strings.Lines(text) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, "\r\n"))
		}
	}
	for _, re := range []*regexp.Regexp{errorNameRe, failureWordRe} {
		for _, line := range lines {
			if re.MatchString(line) {
				return Clean(line, limit)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return Clean(lines[len(lines)-1], limit)
}

// pyDumps re-writes a JSON text the way Python's json.dumps(…, ensure_ascii=False) writes it:
// keys in their order, ", " between items and ": " after a key. A text that is not JSON is
// returned as it is.
func pyDumps(text string) string {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var b strings.Builder
	if !pyWriteValue(dec, &b) {
		return text
	}
	return b.String()
}

// pyWriteValue writes the next JSON value of dec to b; false when the text is not JSON.
func pyWriteValue(dec *json.Decoder, b *strings.Builder) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	switch v := tok.(type) {
	case json.Delim:
		closing := map[json.Delim]string{'{': "}", '[': "]"}[v]
		b.WriteString(string(v))
		for i := 0; dec.More(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if v == '{' {
				key, err := dec.Token()
				if err != nil {
					return false
				}
				b.WriteString(pyString(key.(string)))
				b.WriteString(": ")
			}
			if !pyWriteValue(dec, b) {
				return false
			}
		}
		if _, err := dec.Token(); err != nil {
			return false
		}
		b.WriteString(closing)
	case string:
		b.WriteString(pyString(v))
	case json.Number:
		b.WriteString(v.String())
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case nil:
		b.WriteString("null")
	}
	return true
}

// pyString is a JSON string without HTML escaping.
func pyString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}
