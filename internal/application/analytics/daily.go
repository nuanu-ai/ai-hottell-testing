package analytics

import (
	"maps"
	"slices"
	"time"
)

// SessionDay is one day of a session: a UTC day, or a day of the zone the dataset is asked in.
type SessionDay struct {
	Date string `json:"date"`
	// CostUSD is the cost of the day's requests that have one, nil when none has.
	CostUSD *float64 `json:"cost_usd"`
	// Reqs and Tok are the day's requests and their tokens; Reqs is nil when the session has no
	// request, Tok when the day has none.
	Reqs *int64      `json:"reqs"`
	Tok  *SessionTok `json:"tok"`
	// AgentMin is the agent's active time of the day (AgentMinutesByDay), UserMin the person's
	// pauses that end on it.
	AgentMin float64 `json:"agent_min"`
	UserMin  float64 `json:"user_min"`
	Calls    int     `json:"calls"`
	Errors   int     `json:"errors"`
}

// daily lays b out by the days of the zone loc, oldest first, as the colleague's session_daily
// did by UTC day: the requests by their time, the agent's minutes, the person's pauses by their
// prompt and the calls by their start. A day's reqs count the responses a Codex rollout turn
// stands for, so that the days add up to the session's reqs; its tokens carry no reasoning.
func (b *BuiltSession) daily(loc *time.Location) []SessionDay {
	days := map[string]*SessionDay{}
	day := func(date string) *SessionDay {
		d, ok := days[date]
		if !ok {
			d = &SessionDay{Date: date}
			days[date] = d
		}
		return d
	}
	for _, a := range b.API {
		d := day(DayIn(a.At, loc))
		if d.Reqs == nil {
			d.Reqs = new(int64)
		}
		*d.Reqs += a.Requests()
		if d.Tok == nil {
			d.Tok = &SessionTok{}
		}
		d.Tok.Input += a.Input
		d.Tok.Cached += a.Cached
		d.Tok.Output += a.Output
		if a.Cost != nil {
			if d.CostUSD == nil {
				d.CostUSD = new(float64)
			}
			*d.CostUSD += *a.Cost
		}
	}
	for _, m := range AgentMinutesByDay(b.Turns.List, b.Metrics, loc) {
		day(m.Date).AgentMin += m.Minutes
	}
	for _, g := range b.UserTime.Gaps {
		day(DayIn(g.Prompt.At, loc)).UserMin += g.Min
	}
	for i := range b.Calls.Calls {
		c := &b.Calls.Calls[i]
		d := day(DayIn(c.At, loc))
		d.Calls++
		if c.State == StateError {
			d.Errors++
		}
	}
	out := make([]SessionDay, 0, len(days))
	for _, date := range slices.Sorted(maps.Keys(days)) {
		d := days[date]
		if len(b.API) > 0 && d.Reqs == nil {
			d.Reqs = new(int64)
		}
		if d.CostUSD != nil {
			*d.CostUSD = roundTo(*d.CostUSD, 4)
		}
		d.AgentMin, d.UserMin = roundTo(d.AgentMin, 1), roundTo(d.UserMin, 1)
		out = append(out, *d)
	}
	return out
}

// InZone is the dataset with the daily of each session laid out by the days of the zone loc
// (HT-514); the dataset itself, laid out by UTC day, when loc is nil or UTC. The build does not
// depend on the zone: the days are laid out again from the sessions it keeps, so every zone
// shares one build. The sessions are copied, the dataset is not changed.
func (ds Dataset) InZone(loc *time.Location) Dataset {
	if loc == nil || loc == time.UTC {
		return ds
	}
	built := make(map[SessionKey]*BuiltSession, len(ds.Built))
	for i := range ds.Built {
		built[ds.Built[i].Key] = ds.Built[i].Built
	}
	sessions := slices.Clone(ds.Sessions)
	for i := range sessions {
		s := &sessions[i]
		if b := built[SessionKey{UserID: s.UserID, Agent: s.Agent, SessionID: s.ID}]; b != nil {
			s.Daily = b.daily(loc)
		}
	}
	ds.Sessions = sessions
	ds.Encoded = ds.Encoded.In(loc.String())
	return ds
}
