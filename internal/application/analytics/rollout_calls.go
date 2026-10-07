package analytics

import (
	"slices"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// What a source of a session's data gave, as the sources of the dataset name it.
const (
	// SourceRecorded: the source holds data of the session.
	SourceRecorded = "recorded"
	// SourceMissing: the source holds nothing of the session.
	SourceMissing = "missing"
)

// ApplyCodexFacts sets on each Codex call closed by a PostToolUse what the facts of its session's
// rollout say of it, looked up by tool_use_id = call_id (telemetry.CodexFacts.For): the enrich
// status, the call's facts, the duration when the hook sent none, and the outcome CallState gives
// with them. Without a rollout of the session, as when the person's settings keep transcripts
// from being sent, each call is marked no_transcript and keeps the outcome its output gives. A
// Claude call, or one without a Post, is left as it is. Only numbers and status tokens come from
// the rollout, never the text of an output.
func ApplyCodexFacts(calls []Call, facts telemetry.CodexFacts) {
	for i := range calls {
		c := &calls[i]
		if c.Agent != "codex" || !c.HasPost() {
			continue
		}
		ef, ok := facts.For(telemetry.HookEvent{Agent: "codex", Event: "PostToolUse", ToolUseID: c.ToolUseID})
		if !ok {
			continue
		}
		c.Enrich, c.Facts = ef.Status, ef.Tool
		if c.Facts != nil && c.Facts.DurationMs != nil && (c.Post == nil || c.Post.DurationMs <= 0) {
			d := float64(*c.Facts.DurationMs) / 1000
			c.DurationS = &d
		}
		c.State, c.Note = CallState(c, c.Facts)
	}
}

// TranscriptFactsSource is SourceRecorded when the rollout gave an exit code or a status of any
// call, or the total of a turn among the session's requests (BuildAPI), SourceMissing otherwise.
func TranscriptFactsSource(calls []Call, api []APIRecord) string {
	if slices.ContainsFunc(api, func(a APIRecord) bool { return a.Src == SrcCodexRollout }) {
		return SourceRecorded
	}
	for i := range calls {
		if f := calls[i].Facts; f != nil && (f.ExitCode != nil || f.Status != "") {
			return SourceRecorded
		}
	}
	return SourceMissing
}

// CallCounts are the counts of a session's calls by outcome.
type CallCounts struct {
	Calls int
	// Errors are the calls whose outcome is StateError.
	Errors int
	// Unknown are the calls with no result, StateUnknown.
	Unknown int
	// OutcomeKnown are the calls that succeeded or failed.
	OutcomeKnown int
}

// CountCalls counts calls by outcome.
func CountCalls(calls []Call) CallCounts {
	n := CallCounts{Calls: len(calls)}
	for i := range calls {
		switch calls[i].State {
		case StateError:
			n.Errors++
			n.OutcomeKnown++
		case StateSuccess:
			n.OutcomeKnown++
		case StateUnknown:
			n.Unknown++
		}
	}
	return n
}
