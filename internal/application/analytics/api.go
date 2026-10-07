package analytics

import (
	"slices"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The sources of a model request.
const (
	// SrcClaude: an api_request of Claude Code's native OpenTelemetry.
	SrcClaude = "claude"
	// SrcCodexSSE: a codex.sse_event of kind response.completed linked by conversation.id.
	SrcCodexSSE = "codex_sse"
	// SrcCodexRollout: the total of one turn in the session's Codex rollout, named by the turn's
	// Stop. It stands for all the responses of the turn.
	SrcCodexRollout = "codex_rollout"
)

// sseResponseCompleted is the kind of the codex.sse_event that carries a response's tokens.
const sseResponseCompleted = "response.completed"

// APIRecord is one request to the model, or one turn's requests together.
type APIRecord struct {
	At    time.Time
	Model string
	// Input is all the input tokens, the cached ones included; Cached is the cache read, Created
	// the cache written (Claude only).
	Input, Cached, Created, Output int64
	// Reasoning is nil when the source does not report reasoning tokens (Claude).
	Reasoning *int64
	// Cost is the request's cost in USD: Claude's recorded cost_usd, the tokens at AssumedPrice
	// for Codex; nil when the event recorded no cost.
	Cost *float64
	// PromptID is the Claude prompt and TurnID the Codex turn the record names, empty when it
	// names none.
	PromptID, TurnID string
	// Src is SrcClaude, SrcCodexSSE, SrcCodexRollout or SrcClaudeTranscript.
	Src string
	// Responses is the number of responses a SrcCodexRollout record stands for; 0 is one.
	Responses int64
}

// Requests is how many requests to the model the record stands for.
func (a APIRecord) Requests() int64 { return max(a.Responses, 1) }

// BuildAPI returns the model requests of a session, oldest first: Claude's api_request events,
// the codex.sse_event of kind response.completed, and for each Codex turn whose Stop names it and
// whose rollout total holds at least one response, that turn's total once, however many copies
// of the Stop arrived. The rollout's facts are read once per
// session by telemetry.CodexFacts; this does not parse the rollout again.
func BuildAPI(
	agent string, events []telemetry.HookEvent, claude []telemetry.ClaudeEvent, sse []telemetry.CodexSSE,
	facts telemetry.CodexFacts,
) []APIRecord {
	var api []APIRecord
	for _, e := range claude {
		if e.Event != "api_request" {
			continue
		}
		a := APIRecord{
			At: e.Time, Model: e.Model, Input: e.InputTokens + e.CacheReadTokens + e.CacheCreationTokens,
			Cached: e.CacheReadTokens, Created: e.CacheCreationTokens, Output: e.OutputTokens,
			PromptID: e.PromptID, Src: SrcClaude,
		}
		if e.CostRecorded {
			cost := e.CostUSD
			a.Cost = &cost
		}
		api = append(api, a)
	}
	for _, e := range sse {
		if e.Kind != sseResponseCompleted {
			continue
		}
		reasoning := e.ReasoningTokens
		api = append(api, APIRecord{
			At: e.Time, Model: e.Model, Input: e.InputTokens, Cached: e.CachedTokens, Output: e.OutputTokens,
			Reasoning: &reasoning, Src: SrcCodexSSE, Cost: usageCostOf(e.InputTokens, e.CachedTokens, e.OutputTokens),
		})
	}
	if agent == "codex" {
		// A redelivered Stop may be written twice; the turn's total counts once.
		added := map[string]bool{}
		for _, ev := range events {
			if ev.Event != "Stop" || added[ev.TurnID] {
				continue
			}
			ef, ok := facts.For(ev)
			if !ok || ef.Turn == nil || ef.Turn.Responses == 0 {
				continue
			}
			added[ev.TurnID] = true
			turn := ef.Turn
			model := turn.Model
			if model == "" {
				model = ev.Model
			}
			reasoning := turn.Reasoning
			api = append(api, APIRecord{
				At: ev.Time, Model: model, Input: turn.Input, Cached: turn.Cached, Output: turn.Output,
				Reasoning: &reasoning, TurnID: ev.TurnID, Src: SrcCodexRollout, Responses: turn.Responses,
				Cost: usageCostOf(turn.Input, turn.Cached, turn.Output),
			})
		}
	}
	slices.SortStableFunc(api, func(a, b APIRecord) int { return a.At.Compare(b.At) })
	return api
}

// TurnTokens are the tokens of a turn's requests.
type TurnTokens struct {
	Input, Cached, Output int64
}

// attachAPI keeps the requests that count and adds each to the turn open at its time. Two
// sources of Codex tokens never add up: a codex.sse_event inside a turn whose total the rollout
// gives is dropped.
func (ts *Turns) attachAPI(api []APIRecord) []APIRecord {
	whole := map[*Turn]bool{}
	for _, a := range api {
		if a.Src == SrcCodexRollout {
			if t := ts.At(a.At, a.TurnID); t != nil {
				whole[t] = true
			}
		}
	}
	kept := make([]APIRecord, 0, len(api))
	for _, a := range api {
		if a.Src == SrcCodexSSE && whole[ts.At(a.At, "")] {
			continue
		}
		kept = append(kept, a)
	}
	for _, a := range kept {
		t := ts.At(a.At, a.PromptID)
		if t == nil {
			continue
		}
		if a.At.After(t.Last) {
			t.Last = a.At
		}
		t.Tok.Input += a.Input
		t.Tok.Cached += a.Cached
		t.Tok.Output += a.Output
	}
	return kept
}

// Tokens are a session's tokens. Reasoning is nil when no request reported it.
type Tokens struct {
	Input, Cached, Output int64
	Reasoning             *int64
}

// SessionTokens sums the requests: nil tokens and 0 requests when there are none. A rollout
// turn counts as many requests as its responses.
func SessionTokens(api []APIRecord) (tok *Tokens, reqs int64) {
	if len(api) == 0 {
		return nil, 0
	}
	tok = &Tokens{}
	for _, a := range api {
		tok.Input += a.Input
		tok.Cached += a.Cached
		tok.Output += a.Output
		if a.Reasoning != nil {
			if tok.Reasoning == nil {
				tok.Reasoning = new(int64)
			}
			*tok.Reasoning += *a.Reasoning
		}
		reqs += a.Requests()
	}
	return tok, reqs
}

// SessionModel is the session's model: the commonest of Claude's requests, else the commonest of
// the hook events; "" when none names one. A tie goes to the model seen first.
func SessionModel(api []APIRecord, events []telemetry.HookEvent) string {
	var models []string
	for _, a := range api {
		if a.Src == SrcClaude && a.Model != "" {
			models = append(models, a.Model)
		}
	}
	if m := mostCommon(models); m != "" {
		return m
	}
	models = models[:0]
	for _, ev := range events {
		if ev.Model != "" {
			models = append(models, ev.Model)
		}
	}
	return mostCommon(models)
}

// OTelSource is the state of a session's native OpenTelemetry. Claude: "recorded" with an
// api_request or a metric, else "missing". Codex: "linked" with a codex.sse_event among the
// requests BuildAPI returned (before the rollout's turns replaced any), else "unlinked".
func OTelSource(agent string, built []APIRecord, metrics []telemetry.ClaudeMetric) string {
	if agent == "claude" {
		if len(metrics) > 0 || slices.ContainsFunc(built, func(a APIRecord) bool { return a.Src == SrcClaude }) {
			return SourceRecorded
		}
		return SourceMissing
	}
	if slices.ContainsFunc(built, func(a APIRecord) bool { return a.Src == SrcCodexSSE }) {
		return "linked"
	}
	return "unlinked"
}

// mostCommon is the commonest value, the first seen of a tie; "" for none.
func mostCommon(values []string) string {
	counts := map[string]int{}
	best := ""
	for _, v := range values {
		counts[v]++
	}
	for _, v := range values {
		if counts[v] > counts[best] || best == "" {
			best = v
		}
	}
	return best
}
