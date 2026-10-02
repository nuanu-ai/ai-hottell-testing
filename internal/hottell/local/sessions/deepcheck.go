package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// DeepCheckIn is a Deep report and the frozen source it was written against.
type DeepCheckIn struct {
	SessionID string         `json:"session_id" jsonschema:"id, a unique prefix of it or the path of the transcript, as session_read takes it"`
	Agent     string         `json:"agent,omitempty" jsonschema:"claude or codex; helps when the id is ambiguous"`
	SHA256    string         `json:"source_sha256" jsonschema:"session_source's source_sha256 the report was written against"`
	Records   int            `json:"source_records" jsonschema:"session_source's source_records"`
	Report    map[string]any `json:"report" jsonschema:"the Deep v2 report the agent will send to deep_submit"`
}

// DeepCheckOut is the verdict: ok, or every error found, in the order of the tasks.
type DeepCheckOut struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors"`
}

// The errors of a frozen prefix that is no longer there (mcp.md, «deep_source_check»).
const (
	sourceShorter     = "source shorter than frozen prefix"
	sourceHashChanged = "source hash changed"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The roles of a source line that a Deep report may rest on.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// DeepSourceCheck checks a Deep v2 report against its frozen source, in the order
// docs/specs/hottell-contract/mcp.md («deep_source_check») gives: the prefix is hashed again
// as session_source hashes it, and fewer complete lines than source_records or another hash
// stop the check; then the report's form (deepv2.ValidateDeep), whose error stops it too;
// then the roles of the lines — each task's start_line and at least one goal_evidence line
// a user-role message, each claim's line an assistant message. The session is found as
// session_read finds it, so one Roots.Allow refuses does not exist here either; that, a
// broken source line and arguments out of shape are errors of the call, not of the report.
func DeepSourceCheck(ctx context.Context, r Roots, in DeepCheckIn, now time.Time) (DeepCheckOut, error) {
	if !sha256Hex.MatchString(in.SHA256) {
		return DeepCheckOut{}, errors.New("source_sha256: 64 lowercase hex digits")
	}
	if in.Records < 1 {
		return DeepCheckOut{}, fmt.Errorf("source_records: a positive count, not %d", in.Records)
	}
	// Through JSON once more, so that the numbers are json.Number as deepv2 reads them.
	raw, err := json.Marshal(in.Report)
	if err != nil {
		return DeepCheckOut{}, fmt.Errorf("report: %w", err)
	}
	report, err := deepv2.Decode(raw)
	if err != nil {
		return DeepCheckOut{}, fmt.Errorf("report: %w", err)
	}
	si, err := Find(ctx, r, in.Agent, in.SessionID, now)
	if err != nil {
		return DeepCheckOut{}, err
	}
	// The lines are taken loosely here, to read their roles in the one pass over the
	// prefix; they count only once the form has passed.
	var lines deepLines
	_ = json.Unmarshal(raw, &lines)
	roles, verdict, err := prefixRoles(ctx, si, in, lines.wanted())
	if err != nil {
		return DeepCheckOut{}, err
	}
	if verdict != "" {
		return refused(verdict), nil
	}
	var invalid *deepv2.ValidationError
	if err := deepv2.ValidateDeep(report, si.ID, in.SHA256, in.Records); errors.As(err, &invalid) {
		return refused(invalid.Error()), nil
	} else if err != nil {
		return DeepCheckOut{}, fmt.Errorf("report: %w", err)
	}
	errs := []string{}
	for _, task := range lines.Tasks {
		if roles[task.StartLine] != roleUser {
			errs = append(errs, task.TaskID+": task starts outside a user-role message")
		}
		grounded := false
		for _, ref := range task.GoalEvidence {
			if n, ok := refLine(ref); ok && roles[n] == roleUser {
				grounded = true
			}
		}
		if !grounded {
			errs = append(errs, task.TaskID+": goal is not grounded in a user-role message")
		}
		for _, c := range task.Claims {
			if roles[c.Line] != roleAssistant {
				errs = append(errs, task.TaskID+": completion claim is not an assistant message")
			}
		}
	}
	return DeepCheckOut{OK: len(errs) == 0, Errors: errs}, nil
}

func refused(e string) DeepCheckOut { return DeepCheckOut{Errors: []string{e}} }

// deepLines is the part of a Deep report whose lines this check reads.
type deepLines struct {
	Tasks []struct {
		TaskID       string   `json:"task_id"`
		StartLine    int      `json:"start_line"`
		GoalEvidence []string `json:"goal_evidence"`
		Claims       []struct {
			Line int `json:"line"`
		} `json:"claims"`
	} `json:"tasks"`
}

// wanted is every line whose role the tasks rest on.
func (d deepLines) wanted() map[int]bool {
	w := map[int]bool{}
	for _, task := range d.Tasks {
		w[task.StartLine] = true
		for _, ref := range task.GoalEvidence {
			if n, ok := refLine(ref); ok {
				w[n] = true
			}
		}
		for _, c := range task.Claims {
			w[c.Line] = true
		}
	}
	return w
}

// refLine is the line an L<n> evidence reference points at.
func refLine(ref string) (int, bool) {
	digits, ok := bytes.CutPrefix([]byte(ref), []byte("L"))
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(string(digits))
	return n, err == nil && n > 0
}

// prefixRoles hashes the first in.Records lines of the transcript, each of which must end in
// "\n", and returns the role of each wanted line among them. verdict is sourceShorter or
// sourceHashChanged when the prefix is not the one in names, "" when it is. It reads no
// further than the prefix.
func prefixRoles(ctx context.Context, si Info, in DeepCheckIn, wanted map[int]bool) (map[int]string, string, error) {
	rc, err := open(ctx, si)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	digest := sha256.New()
	roles, records := map[int]string{}, 0
	err = eachLine(rc, rc.maxLine, func(n int, raw []byte) bool {
		if !bytes.HasSuffix(raw, []byte("\n")) {
			return false
		}
		digest.Write(raw)
		records = n
		if wanted[n] {
			roles[n] = lineRole(si.Agent, raw)
		}
		return n < in.Records
	})
	if tl, ok := asTooLarge(err, si.Path); ok {
		return nil, "", tl
	}
	if err != nil {
		return nil, "", fmt.Errorf("source of %s: %w", si.Path, err)
	}
	switch {
	case records < in.Records:
		return nil, sourceShorter, nil
	case hex.EncodeToString(digest.Sum(nil)) != in.SHA256:
		return nil, sourceHashChanged, nil
	}
	return roles, "", nil
}

// lineRole is roleUser, roleAssistant or "" for one line of agent's transcript, by the table
// of mcp.md, «deep_source_check».
func lineRole(agent string, raw []byte) string {
	if agent == "codex" {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw, &rec) != nil || rec.Type != "response_item" || rec.Payload.Type != "message" {
			return ""
		}
		switch rec.Payload.Role {
		case roleAssistant:
			return roleAssistant
		case roleUser:
			for _, c := range rec.Payload.Content {
				if !codexInsert(c.Text) {
					return roleUser
				}
			}
		}
		return ""
	}
	var rec struct {
		Type             string `json:"type"`
		IsCompactSummary bool   `json:"isCompactSummary"`
		Message          struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &rec) != nil {
		return ""
	}
	switch {
	case rec.Type == "assistant" && rec.Message.Role == roleAssistant &&
		hasBlock(rec.Message.Content, func(t string) bool { return t == "text" }):
		return roleAssistant
	case rec.Type == "user" && rec.Message.Role == roleUser && !rec.IsCompactSummary &&
		spoken(rec.Message.Content):
		return roleUser
	}
	return ""
}

// codexInserts are the wrappers of what Codex itself writes into a user-role message: the
// environment, the user instructions of config.toml and the AGENTS.md instructions, each an
// opening and its closing (HT-383).
//
//nolint:gochecknoglobals // read-only table
var codexInserts = [][2]string{
	{"<environment_context>", "</environment_context>"},
	{"<user_instructions>", "</user_instructions>"},
	{"# AGENTS.md instructions for ", "</INSTRUCTIONS>"},
}

// codexInsert reports whether a text of a Codex user message is one of Codex's own inserts,
// whole: it opens and closes with one wrapper, spaces around aside. Empty text is no
// request either.
func codexInsert(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	for _, w := range codexInserts {
		if strings.HasPrefix(t, w[0]) && strings.HasSuffix(t, w[1]) {
			return true
		}
	}
	return false
}

// spoken reports whether a Claude Code user record's content is a person's reply: a string,
// or blocks not all of which are tool_result.
func spoken(content json.RawMessage) bool {
	var str string
	if json.Unmarshal(content, &str) == nil {
		return true
	}
	return hasBlock(content, func(t string) bool { return t != "tool_result" })
}

// hasBlock reports whether a Claude Code message's content holds a block whose type is one
// of what. An assistant record of tool_use (and thinking) blocks alone is a tool call, as
// Codex's function_call is, and not a message (mcp.md, «deep_source_check»).
func hasBlock(content json.RawMessage, what func(blockType string) bool) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if what(b.Type) {
			return true
		}
	}
	return false
}
