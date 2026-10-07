package analytics

import (
	"crypto/sha1" //nolint:gosec // an identity of the input, as the colleague's builder computes it; not a security hash
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// Call is one tool call of a session: its PreToolUse, the PostToolUse or PostToolUseFailure that
// closed it, and the native tool_result of Claude Code, joined by tool_use_id.
type Call struct {
	Agent     string
	ToolUseID string
	// At is the time of the PreToolUse, or of the Post of a call without one.
	At time.Time
	// Tool is the tool as the hook names it, "?" when it names none; Name is it as shown
	// (DisplayTool).
	Tool string
	Name string
	// Input is the tool's input as the JSON text the agent sent; Args is it as an object, empty
	// when it is not one, and nil in a session BuildSession built, which reads it and lets it go.
	Input string
	Args  map[string]any
	// InputHash identifies the call's tool and whole input: "<tool>:<hash>".
	InputHash      string
	AgentID        string
	AgentType      string
	PermissionMode string
	// MCP is the MCP server of the call, "" for none (MCPServerOf).
	MCP      string
	TurnID   string
	PromptID string
	// Cmd is the shell command of a Bash, shell or exec_command call, "" otherwise.
	Cmd string
	// IsEdit marks a call that changes files; IsWait one that waits on a process, an agent or the
	// clock.
	IsEdit bool
	IsWait bool
	// NoPre marks a call known only by its Post.
	NoPre bool

	// PostAt is the time of the Post that closed the call; zero while none did.
	PostAt time.Time
	// Failed marks a call closed by PostToolUseFailure. Failure is the hook's error text, empty when
	// the hook sent none.
	Failed  bool
	Failure string
	// RespHead, RespTail, RespLen and RespHash are the digest of the tool's output (HookEvent).
	RespHead string
	RespTail string
	RespLen  int64
	RespHash string
	// DurationS is the call's duration in seconds: the native tool_result's, else the Post's
	// duration_ms, else the time from Pre to Post; nil while none is known.
	DurationS *float64
	// NativeSuccess and NativeError are the success and error of Claude Code's tool_result; nil
	// and "" without one.
	NativeSuccess *bool
	NativeError   string

	// State is the call's outcome (StateSuccess, StateError, StateDone or StateUnknown) and Note a
	// short reason for it, as CallState gives them.
	State string
	Note  string

	// Enrich is the enrich status of a Codex call (telemetry.EnrichFound and the like) and Facts
	// what the session's rollout tells of it; "" and nil until ApplyCodexFacts sets them.
	Enrich string
	Facts  *telemetry.ToolFact

	// Pre is the hook event the call was made from: its PreToolUse, or its Post when NoPre. Post
	// is the event that closed it, nil while none did. Both point into the session's events, which
	// the dataset keeps anyway: a copy of each would double the memory of the calls (HT-529).
	Pre  *telemetry.HookEvent
	Post *telemetry.HookEvent
}

// PreEvent is the event the call was made from; zero for a call made by hand without one.
func (c *Call) PreEvent() telemetry.HookEvent {
	if c.Pre == nil {
		return telemetry.HookEvent{}
	}
	return *c.Pre
}

// HasPost tells whether a Post closed the call.
func (c *Call) HasPost() bool { return !c.PostAt.IsZero() }

// Calls are the tool calls of one session and what their assembly dropped.
type Calls struct {
	// Calls are in the order of their start.
	Calls []Call
	// DupPre counts the repeated PreToolUse of a tool_use_id, dropped.
	DupPre int
	// OrphanPosts counts the Posts with no PreToolUse, each a call marked NoPre.
	OrphanPosts int
}

// BuildCalls assembles the tool calls of one session of agent from its hook events, oldest first,
// and the native events of Claude Code (nil for Codex). A PreToolUse makes a call, once per
// tool_use_id; the first PostToolUse or PostToolUseFailure of that id closes it, and one with no
// PreToolUse makes a call of its own; Claude's tool_result sets the success and the duration.
// Each call's State and Note are CallState's without the facts of a rollout.
func BuildCalls(agent string, events []telemetry.HookEvent, native []telemetry.ClaudeEvent) Calls {
	var out Calls
	var calls []*Call
	byID := map[string]*Call{}
	var posts []*telemetry.HookEvent
	for i := range events {
		ev := &events[i]
		switch ev.Event {
		case "PreToolUse":
			key := ev.ToolUseID
			if key == "" {
				key = "pre@" + ev.Time.Format(time.RFC3339Nano)
			}
			if _, seen := byID[key]; seen {
				out.DupPre++
				continue
			}
			c := newCall(agent, ev, true)
			byID[key] = c
			calls = append(calls, c)
		case "PostToolUse", "PostToolUseFailure":
			posts = append(posts, ev)
		}
	}
	for _, ev := range posts {
		var c *Call
		if ev.ToolUseID != "" {
			c = byID[ev.ToolUseID]
		}
		if c == nil {
			out.OrphanPosts++
			c = newCall(agent, ev, false)
			key := ev.ToolUseID
			if key == "" {
				key = "post@" + ev.Time.Format(time.RFC3339Nano)
			}
			byID[key] = c
			calls = append(calls, c)
		}
		if c.HasPost() {
			continue
		}
		closeCall(c, ev)
	}
	for _, ev := range native {
		if ev.Event != "tool_result" || ev.ToolUseID == "" {
			continue
		}
		c, ok := byID[ev.ToolUseID]
		if !ok {
			continue
		}
		success := ev.Success
		c.NativeSuccess = &success
		c.NativeError = ev.Error
		if ev.DurationMs > 0 {
			d := float64(ev.DurationMs) / 1000
			c.DurationS = &d
		}
	}
	slices.SortStableFunc(calls, func(a, b *Call) int { return a.At.Compare(b.At) })
	out.Calls = make([]Call, 0, len(calls))
	for _, c := range calls {
		c.State, c.Note = CallState(c, nil)
		out.Calls = append(out.Calls, *c)
	}
	return out
}

// newCall is the call ev opens: a PreToolUse, or a Post without one when pre is false.
func newCall(agent string, ev *telemetry.HookEvent, pre bool) *Call {
	tool := ev.Tool
	if tool == "" {
		tool = "?"
	}
	hash := ev.ToolInputHash
	if hash == "" && pre {
		sum := sha1.Sum([]byte(ev.ToolInput)) //nolint:gosec // see the import
		hash = hex.EncodeToString(sum[:])
	}
	c := &Call{
		Agent: agent, ToolUseID: ev.ToolUseID, At: ev.Time, Tool: tool, Name: DisplayTool(tool),
		Input: ev.ToolInput, InputHash: ev.Tool + ":" + hash,
		AgentID: ev.AgentID, AgentType: ev.AgentType, PermissionMode: ev.PermissionMode,
		MCP: MCPServerOf(ev.Tool, ev.McpServer), TurnID: ev.TurnID, PromptID: ev.PromptID,
		IsEdit: isEditTool(tool), NoPre: !pre, Pre: ev, Args: map[string]any{},
	}
	c.IsWait = isPollTool(c.Name) || isPollTool(tool)
	if args := toolArgs(ev.ToolInput); args != nil {
		c.Args = args
	}
	if tool == "Bash" || tool == "shell" || tool == "exec_command" {
		for _, key := range []string{"command", "cmd"} {
			if cmd, ok := c.Args[key].(string); ok && cmd != "" {
				c.Cmd = cmd
				break
			}
		}
	}
	return c
}

// toolArgs is the JSON object of a tool's input. The server keeps only the first
// HookToolInputMax characters of it, so a long input is cut and no longer valid JSON: then the
// keys read whole before the cut are kept. Nil when the input is no object.
func toolArgs(input string) map[string]any {
	var args map[string]any
	if input == "" {
		return nil
	}
	if json.Unmarshal([]byte(input), &args) == nil {
		return args
	}
	dec := json.NewDecoder(strings.NewReader(input))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	args = map[string]any{}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			break
		}
		var v any
		if dec.Decode(&v) != nil {
			break
		}
		args[key] = v
	}
	return args
}

// closeCall closes c by the Post ev: its time, output digest, failure and duration.
func closeCall(c *Call, ev *telemetry.HookEvent) {
	c.PostAt, c.Post = ev.Time, ev
	c.Failed = ev.Event == "PostToolUseFailure"
	if c.Failed {
		c.Failure = ev.Error
	}
	c.RespHead, c.RespTail = ev.ToolResponse, ev.ToolResponseTail
	c.RespLen, c.RespHash = ev.ToolResponseLen, ev.ToolResponseHash
	d := ev.Time.Sub(c.At).Seconds()
	if ev.DurationMs > 0 {
		d = float64(ev.DurationMs) / 1000
	}
	c.DurationS = &d
}

// isPollTool lists the tools that wait on something else: a running process, an agent, the clock.
func isPollTool(name string) bool {
	switch name {
	case "write_stdin", "collaboration.wait_agent", "clock.sleep", "wait", "sleep":
		return true
	}
	return false
}
