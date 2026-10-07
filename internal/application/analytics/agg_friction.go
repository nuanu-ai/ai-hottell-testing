package analytics

import (
	"slices"
)

// FrictionRow is one friction signal of the dataset: its episodes over the sessions.
type FrictionRow struct {
	Key  string   `json:"key"`
	Name string   `json:"name"`
	How  string   `json:"how"`
	Sev  Severity `json:"sev"`
	// Sessions are the ids of the sessions with an episode, sorted; BySession their episodes.
	Sessions  []string       `json:"sessions"`
	BySession map[string]int `json:"by_session"`
	// Count is the number of episodes; nil for correction, which Deep marks.
	Count *int `json:"count"`
	// CostUSD is the cost of the episodes: coldcache only, and only when every episode has one.
	CostUSD *float64 `json:"cost_usd"`
	// Evidence are the MaxFrictionEvidence newest lines of the episodes, in time order.
	Evidence []Evidence `json:"evidence"`
}

// SessionFriction runs the friction rules over a built session and numbers the evidence by the
// session's timeline: an evidence line takes the first timeline event of its instant.
func SessionFriction(b *BuiltSession) Friction {
	sid, calls := b.Key.SessionID, b.Calls.Calls
	f := Friction{
		FrictionRetry:     DetectRetry(sid, calls),
		FrictionThrash:    DetectThrash(sid, calls),
		FrictionMCPFail:   DetectMCPFail(sid, calls),
		FrictionColdCache: DetectColdCache(sid, b.Prompts, b.API),
		FrictionCompact:   CompactFriction(sid, b.Compactions),
		FrictionReread:    RereadFriction(sid, calls, b.Compactions, b.Cwd),
		FrictionWait:      WaitFriction(sid, b.Waits),
		FrictionAbort:     AbortFriction(sid, b.Events),
	}
	lineAt := map[string]int{}
	for _, ev := range b.Timeline.Events {
		if _, seen := lineAt[ev.At]; !seen {
			lineAt[ev.At] = ev.Line
		}
	}
	for _, eps := range f {
		for i := range eps {
			for j := range eps[i].Evidence {
				if e := &eps[i].Evidence[j]; e.Line == 0 {
					e.Line = lineAt[e.At]
				}
			}
		}
	}
	return f
}

// AggregateFriction lays the sessions' friction out by signal, in the order of FrictionMeta, as
// the colleague's aggregate_friction with by_session: the episodes by session, count nil for
// correction, the cost of coldcache when all its episodes have one, and the newest evidence.
func AggregateFriction(sessions []*BuiltSession) []FrictionRow {
	out := make([]FrictionRow, 0, len(FrictionMeta()))
	for _, m := range FrictionMeta() {
		r := FrictionRow{Key: m.Key, Name: m.Name, How: m.How, Sev: m.Sev, Sessions: []string{}, BySession: map[string]int{}}
		var eps []FrictionEpisode
		for _, b := range sessions {
			got := b.Friction[m.Key]
			if len(got) == 0 {
				continue
			}
			eps = append(eps, got...)
			if r.BySession[b.Key.SessionID] == 0 {
				r.Sessions = append(r.Sessions, b.Key.SessionID)
			}
			r.BySession[b.Key.SessionID] += len(got)
		}
		slices.Sort(r.Sessions)
		r.Sessions = slices.Compact(r.Sessions)
		if m.Key != FrictionCorrection {
			n := len(eps)
			r.Count = &n
		}
		if m.Key == FrictionColdCache && len(eps) > 0 {
			total, known := 0.0, true
			for _, ep := range eps {
				if ep.Cost == nil {
					known = false
					break
				}
				total += *ep.Cost
			}
			if known {
				total = roundTo(total, 4)
				r.CostUSD = &total
			}
		}
		r.Evidence = FrictionEvidence(eps)
		out = append(out, r)
	}
	return out
}
