package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The kinds of transcript file a source line belongs to, as the store names them.
const (
	SrcKindMain     = "main"
	SrcKindSubagent = "subagent"
)

// SourceLine is the line of a transcript a timeline event is built from: its number in the file,
// from 1, as a Deep review's L<n> counts it, and the kind of the file.
type SourceLine struct {
	Line int
	Kind string
}

// SourceLines are the lines of a session's transcript its timeline events are built from, by the
// id the event and the line share (HT-410). A map is nil when the transcript names none.
type SourceLines struct {
	// Calls are the lines of the calls by tool_use id (Claude) or call_id (Codex; for a call of
	// code mode, the id of its item_completed).
	Calls map[string]SourceLine
	// Prompts are the lines of Claude's prompts by promptId: the first user line of it that is
	// not a tool's result.
	Prompts map[string]SourceLine
	// TurnStarts and TurnEnds are the lines of Codex's turns by turn_id: its task_started, and
	// the task_complete or turn_aborted that ends it.
	TurnStarts map[string]SourceLine
	TurnEnds   map[string]SourceLine
}

// put sets m[id] to line unless id is empty or already there, and returns m.
func put(m map[string]SourceLine, id string, line SourceLine) map[string]SourceLine {
	if id == "" {
		return m
	}
	if m == nil {
		m = map[string]SourceLine{}
	}
	if _, ok := m[id]; !ok {
		m[id] = line
	}
	return m
}

// CodexSourceLines are the source lines of a Codex session from the lines of its main rollout
// that the facts read: the calls by call_id, else by the id of their item_completed, the turns'
// starts and ends by turn_id. A line that is not a JSON record is skipped.
func CodexSourceLines(lines telemetry.Lines) SourceLines {
	var src SourceLines
	for _, l := range lines {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				TurnID string `json:"turn_id"`
				Item   struct {
					ID string `json:"id"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(l.Body), &rec) != nil {
			continue
		}
		at := SourceLine{Line: int(l.Number), Kind: SrcKindMain} //nolint:gosec // a line number fits an int
		switch p := rec.Payload; {
		case rec.Type == "response_item" &&
			(p.Type == "function_call" || p.Type == "custom_tool_call" || p.Type == "local_shell_call"):
			src.Calls = put(src.Calls, p.CallID, at)
		case rec.Type == "event_msg" && p.Type == "item_completed":
			// A call of code mode is in the rollout only by its item_completed; a call with its
			// own record keeps that earlier line.
			src.Calls = put(src.Calls, p.Item.ID, at)
		case rec.Type == "event_msg" && p.Type == "task_started":
			src.TurnStarts = put(src.TurnStarts, p.TurnID, at)
		case rec.Type == "event_msg" && (p.Type == "task_complete" || p.Type == "turn_aborted"):
			src.TurnEnds = put(src.TurnEnds, p.TurnID, at)
		}
	}
	return src
}

// ClaudeSourceLines are the source lines of the Claude session sid from refs, the lines of one
// person's files ordered by file and line: the calls by tool_use id in any file, the prompts by
// promptId in the main file only, as a subagent's lines repeat the promptId of the prompt that
// started it. The main file is the session's own, <sid>.jsonl, when refs name it, else the main
// file with the most lines named; the other main files are left out, as their numbers would
// mix.
func ClaudeSourceLines(sid string, refs []telemetry.SourceRef) SourceLines {
	main := claudeMainFile(sid, refs)
	var src SourceLines
	for _, r := range refs {
		if r.Kind == SrcKindMain && r.File != main || r.Kind != SrcKindMain && r.Kind != SrcKindSubagent {
			continue
		}
		at := SourceLine{Line: int(r.Number), Kind: r.Kind} //nolint:gosec // a line number fits an int
		for _, id := range r.ToolUseIDs {
			src.Calls = put(src.Calls, id, at)
		}
		if r.Kind == SrcKindMain {
			src.Prompts = put(src.Prompts, r.PromptID, at)
		}
	}
	return src
}

// claudeMainFile is the main file of the session sid among refs: <sid>.jsonl when named, else the
// main file named by the most refs, the first of a tie; "" without a main file.
func claudeMainFile(sid string, refs []telemetry.SourceRef) string {
	count := map[string]int{}
	best := ""
	for _, r := range refs {
		if r.Kind != SrcKindMain {
			continue
		}
		if r.File == sid+".jsonl" {
			return r.File
		}
		count[r.File]++
		if best == "" || count[r.File] > count[best] {
			best = r.File
		}
	}
	return best
}

// readClaudeSource reads the source lines of a Claude session whose first hook event is at start,
// from TranscriptSlack before it, as ClaudeTranscriptAPI bounds its read.
func readClaudeSource(ctx context.Context, src Source, key SessionKey, start time.Time) (SourceLines, error) {
	var since time.Time
	if !start.IsZero() {
		since = start.Add(-TranscriptSlack)
	}
	refs, err := src.ClaudeSourceRefs(ctx, key.UserID, key.SessionID, since)
	if err != nil {
		return SourceLines{}, fmt.Errorf("read claude source lines: %w", err)
	}
	return ClaudeSourceLines(key.SessionID, refs), nil
}

// sourceOf is the source line of the timeline object ref of in, false when the transcript names
// none: a prompt by its Claude promptId or its Codex turn's start, a call by its id, a Stop or an
// abort by the end of its Codex turn.
func sourceOf(in TimelineInput, ref timelineRef) (SourceLine, bool) {
	var line SourceLine
	var ok bool
	switch ref.kind {
	case RefPrompt:
		p := in.Prompts[ref.i]
		if line, ok = in.Src.Prompts[p.PromptID]; !ok && p.TurnID != "" {
			line, ok = in.Src.TurnStarts[p.TurnID]
		}
	case RefCall:
		if id := in.Calls[ref.i].ToolUseID; id != "" {
			line, ok = in.Src.Calls[id]
		}
	case RefStop, RefInterrupt:
		if id := in.Events[ref.i].TurnID; id != "" {
			line, ok = in.Src.TurnEnds[id]
		}
	}
	return line, ok
}
