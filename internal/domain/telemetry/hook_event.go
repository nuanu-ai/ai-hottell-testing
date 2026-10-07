package telemetry

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNoHookPeriod is answered by a read of hook events whose filter has no period: From and To
// are both required and To must be after From, so that a read never scans the whole store.
var ErrNoHookPeriod = errors.New("hook events need a period: from and to, to after from")

// HookWindow is the length of one read of a period of hook events: a period is read window by
// window so that no single read holds the tool payloads of the whole period.
const HookWindow = 4 * time.Hour

// Cuts of the text fields of a HookEvent, in characters (UTF-8 code points).
const (
	// HookToolInputMax is the longest ToolInput kept.
	HookToolInputMax = 4000
	// HookToolResponseHeadMax is the longest ToolResponse kept.
	HookToolResponseHeadMax = 1500
	// HookToolResponseTailMax is the longest ToolResponseTail kept.
	HookToolResponseTailMax = 401
	// HookPromptMax is the longest Prompt kept.
	HookPromptMax = 4000
	// HookLastAssistantMessageMax is the longest LastAssistantMessage kept.
	HookLastAssistantMessageMax = 2000
	// HookErrorMax is the longest Error kept. A measured PostToolUseFailure is about 1 KB whole with
	// a one-line error (docs/specs/hottell-contract/payload-sizes.md); the cut bounds a longer one,
	// such as the output of a failed command.
	HookErrorMax = 1000
)

// HookEvent is one record of hottell-hooks: a hook of Claude Code or Codex as the agent sent it.
// The fields come from the record's JSON body; a field the agent did not send, or the person's
// settings cut out, is empty (zero for DurationMs). Repeats of one event are possible and are
// not merged.
type HookEvent struct {
	Time time.Time
	// UserID is the person the server attributed the record to; uuid.Nil when it did not.
	UserID uuid.UUID
	// Agent is "claude" or "codex".
	Agent string
	// Event is the hook event name, for example "PostToolUse".
	Event     string
	SessionID string
	TurnID    string
	PromptID  string
	ToolUseID string
	Tool      string
	// ToolInput is the tool's input as the JSON text the agent sent, cut to HookToolInputMax.
	ToolInput string
	// ToolInputHash identifies the whole input, which ToolInput may cut.
	ToolInputHash string
	// ToolResponse is the head of the tool's output, cut to HookToolResponseHeadMax. The text of a
	// string response, the JSON text of an object response.
	ToolResponse string
	// ToolResponseTail is the end of the output, at most HookToolResponseTailMax characters.
	ToolResponseTail string
	// ToolResponseLen is the length of the whole output in bytes.
	ToolResponseLen int64
	// ToolResponseHash identifies the whole output; the Codex command preamble (chunk id, wall
	// time, exit code line, original token count) does not enter it, so a repeated command with
	// the same output has the same hash.
	ToolResponseHash string
	// DurationMs is the tool's duration; zero when the agent did not send it.
	DurationMs int64
	Cwd        string
	// RepoRoot and RepoRemote are the git repository of Cwd the binary sends (hottell.repo.root,
	// the common git directory, and hottell.repo.remote, its origin without credentials); empty
	// outside a repository and in records of binaries before HT-506.
	RepoRoot             string
	RepoRemote           string
	Model                string
	PermissionMode       string
	AgentType            string
	AgentID              string
	McpServer            string
	SessionTitle         string
	Source               string
	Reason               string
	Trigger              string
	Prompt               string
	LastAssistantMessage string
	// Error is the error text of a PostToolUseFailure, cut to HookErrorMax; empty for any other
	// event.
	Error string
}

// PulseWindow is how far back the bars of a Pulse go.
const PulseWindow = 10 * time.Minute

// PulseBarWidth is the width of one bar of a Pulse.
const PulseBarWidth = 10 * time.Second

// PulseActiveWindow is how recently a session must have sent a hook to count as active.
const PulseActiveWindow = 2 * time.Minute

// PulseActiveLimit is the most active sessions a Pulse lists.
const PulseActiveLimit = 20

// PulseQuery is what a Pulse is counted for: the moment Now, and the person and the agent when
// set; uuid.Nil and "" count every person and both agents.
type PulseQuery struct {
	Now    time.Time
	UserID uuid.UUID
	Agent  string
}

// Pulse is the live state of the hooks: how many events arrived per bar over the last
// PulseWindow and which sessions are active now, for the person and agent of its PulseQuery.
type Pulse struct {
	// At is the moment the Pulse is counted for.
	At time.Time
	// Bars are the hook events per PulseBarWidth and agent, oldest first.
	Bars []PulseBar
	// Active are the sessions that sent a hook within PulseActiveWindow, the latest first.
	Active []ActiveSession
}

// PulseBar is the number of hook events of one agent in the bar starting at Start.
type PulseBar struct {
	Start time.Time
	Agent string
	Count int
}

// ActiveSession is a session that sent a hook recently.
type ActiveSession struct {
	// UserID is the person of the session.
	UserID    uuid.UUID
	SessionID string
	Agent     string
	// Cwd is the first non-empty working directory seen in the window; empty if none.
	Cwd    string
	LastAt time.Time
	// PerMinute is the number of events in the last minute.
	PerMinute int
}

// Window is a span of time, Start included and End excluded.
type Window struct {
	Start, End time.Time
}

// Windows splits [from, to) into consecutive windows of step, the last one cut at to. It
// returns nothing when to is not after from or step is not positive.
func Windows(from, to time.Time, step time.Duration) []Window {
	if !to.After(from) || step <= 0 {
		return nil
	}
	var windows []Window
	for start := from; start.Before(to); start = start.Add(step) {
		end := start.Add(step)
		if end.After(to) {
			end = to
		}
		windows = append(windows, Window{Start: start, End: end})
	}
	return windows
}
