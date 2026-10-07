package analytics

import (
	"slices"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// costBasisMixed is the summary's basis when some of the costs are estimates.
const costBasisMixed = "estimate"

// Summary is the dataset's summary over the sessions it shows: how complete the data is, the
// first steps of the improvement cycle and the sums of the window.
type Summary struct {
	Coverage Coverage     `json:"coverage"`
	Funnel   Funnel       `json:"funnel"`
	Period   PeriodTotals `json:"period"`
}

// Coverage is «data completeness»: hooks from the start, a known call outcome, a recorded cost.
// CostUSD is nil and CostBasis empty when no session has a cost.
type Coverage struct {
	Sessions     int      `json:"sessions"`
	HooksFull    int      `json:"hooks_full"`
	Calls        int      `json:"calls"`
	OutcomeKnown int      `json:"outcome_known"`
	CostKnown    int      `json:"cost_known"`
	CostUSD      *float64 `json:"cost_usd"`
	CostBasis    *string  `json:"cost_basis"`
}

// Funnel are the first three steps of the improvement cycle: the sessions of the person's work
// (kinds user and automation), the friction episodes and the cards about the work.
type Funnel struct {
	Sessions int `json:"sessions"`
	Signals  int `json:"signals"`
	Topics   int `json:"topics"`
}

// PeriodTotals are the sums over the part of the sessions inside the window. CostUSD is nil when
// no request inside the window has a cost. UserMin is the person's time: an agent's session
// (SessionKindAgent) has no person at its prompts and does not count in it.
type PeriodTotals struct {
	CostUSD  *float64 `json:"cost_usd"`
	AgentMin float64  `json:"agent_min"`
	UserMin  float64  `json:"user_min"`
}

// BuildSummary summarises the sessions a dataset shows over its window. signals is the number of
// friction episodes of the sessions; findings are the dataset's cards.
func BuildSummary(sessions []SessionBuild, findings []Finding, signals int, win Window) Summary {
	var sm Summary
	sm.Funnel.Signals = signals
	for _, f := range findings {
		if f.Scope != ScopeCollection {
			sm.Funnel.Topics++
		}
	}
	var costSum, periodCost float64
	periodCosted := false
	allOTel := true
	for i := range sessions {
		s := &sessions[i]
		sm.Coverage.Sessions++
		if !s.Partial.Partial {
			sm.Coverage.HooksFull++
		}
		if s.Kind == SessionKindUser || s.Kind == SessionKindAutomation {
			sm.Funnel.Sessions++
		}
		for _, c := range s.Calls {
			sm.Coverage.Calls++
			if c.State == StateSuccess || c.State == StateError {
				sm.Coverage.OutcomeKnown++
			}
		}
		if cost := SessionCost(s.Turns.API); cost != nil {
			sm.Coverage.CostKnown++
			costSum += *cost
			allOTel = allOTel && CostBasis(s.Turns.API, cost) == CostBasisOTel
		}
		for _, a := range s.Turns.API {
			if a.Cost != nil && inWindow(a.At, win) {
				periodCost += *a.Cost
				periodCosted = true
			}
		}
		sm.Period.AgentMin += agentMinutesIn(s, win)
		if s.Kind != SessionKindAgent {
			sm.Period.UserMin += userMinutesIn(s, win)
		}
	}
	if sm.Coverage.CostKnown > 0 {
		sum := roundTo(costSum, 4)
		basis := costBasisMixed
		if allOTel {
			basis = CostBasisOTel
		}
		sm.Coverage.CostUSD, sm.Coverage.CostBasis = &sum, &basis
	}
	if periodCosted {
		c := roundTo(periodCost, 4)
		sm.Period.CostUSD = &c
	}
	sm.Period.AgentMin = roundTo(sm.Period.AgentMin, 1)
	sm.Period.UserMin = roundTo(sm.Period.UserMin, 1)
	return sm
}

// inWindow tells whether at falls inside [win.From, win.To).
func inWindow(at time.Time, win Window) bool {
	return !at.Before(win.From) && at.Before(win.To)
}

// agentMinutesIn is the session's active time inside the window: Claude Code's
// claude_code.active_time.total of type cli recorded inside it when the session has that metric,
// else the part of its turns inside it.
func agentMinutesIn(s *SessionBuild, win Window) float64 {
	if slices.ContainsFunc(s.Metrics, func(m telemetry.ClaudeMetric) bool { return m.Metric == activeTimeMetric && m.Type == "cli" }) {
		seconds := 0.0
		for _, m := range s.Metrics {
			if m.Metric == activeTimeMetric && m.Type == "cli" && inWindow(m.Time, win) {
				seconds += m.Value
			}
		}
		return seconds / 60
	}
	minutes := 0.0
	for _, t := range s.Turns.List {
		from, to := t.Start, t.Start.Add(time.Duration(t.DurMs)*time.Millisecond)
		if from.Before(win.From) {
			from = win.From
		}
		if to.After(win.To) {
			to = win.To
		}
		if to.After(from) {
			minutes += to.Sub(from).Minutes()
		}
	}
	return minutes
}

// userMinutesIn is the person's pauses before the prompts inside the window.
func userMinutesIn(s *SessionBuild, win Window) float64 {
	minutes := 0.0
	for _, g := range UserGaps(s.Prompts, s.Turns.List, s.Waits).Gaps {
		if inWindow(g.Prompt.At, win) {
			minutes += g.Min
		}
	}
	return minutes
}
