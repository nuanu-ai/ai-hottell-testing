package sessions

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// StatsIn asks for one session (ID) or for a period (the filter and the window).
type StatsIn struct {
	Filter
	ID   string `json:"id,omitempty" jsonschema:"one session; without id the aggregate of the period"`
	From string `json:"from,omitempty" jsonschema:"count only events at or after: YYYY-MM-DD, RFC3339 or a duration back (168h); without since the files are those changed at or after from"`
	To   string `json:"to,omitempty" jsonschema:"count only events before: YYYY-MM-DD (inclusive) or RFC3339"`
}

// ToolStat counts the calls of one tool and the errors they returned.
type ToolStat struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

// Stats aggregates one session or a period.
type Stats struct {
	Sessions      int                    `json:"sessions"`
	Events        int                    `json:"events"`
	ByKind        map[string]int         `json:"by_kind"`
	Turns         int                    `json:"turns"`
	UserMessages  int                    `json:"user_messages"`
	ToolCalls     int                    `json:"tool_calls"`
	ToolErrors    int                    `json:"tool_errors"`
	Tools         []ToolStat             `json:"tools"`
	Tokens        TokenUsage             `json:"tokens"`
	TokensByModel map[string]*TokenUsage `json:"tokens_by_model"`
	First         time.Time              `json:"first,omitzero"`
	Last          time.Time              `json:"last,omitzero"`
	DurationSec   float64                `json:"duration_sec"` // one session: from the first event to the last; a period: the sum over sessions
	PerSession    []Brief                `json:"per_session,omitempty"`
	Coverage      *Coverage              `json:"coverage,omitempty"`
}

// Brief sums up one session of a period.
type Brief struct {
	Agent       string     `json:"agent"`
	ID          string     `json:"id"`
	Cwd         string     `json:"cwd,omitempty"`
	Title       string     `json:"title,omitempty"`
	Events      int        `json:"events"`
	ToolCalls   int        `json:"tool_calls"`
	Tokens      TokenUsage `json:"tokens"`
	DurationSec float64    `json:"duration_sec"`
}

// Coverage tells how complete the aggregate is.
type Coverage struct {
	SessionsFound     int `json:"sessions_found"`     // files matching agent, since/until, project
	SessionsRead      int `json:"sessions_read"`      // read on this page (offset, limit, the page's byte cap)
	SessionsInPeriod  int `json:"sessions_in_period"` // of those, with events inside from/to
	EventsInPeriod    int `json:"events_in_period"`
	EventsOutOfPeriod int `json:"events_out_of_period"`
	Unreadable        int `json:"unreadable,omitempty"`
	NextOffset        int `json:"next_offset,omitempty"` // 0 when every found session was read
}

func newStats() *Stats {
	return &Stats{ByKind: map[string]int{}, TokensByModel: map[string]*TokenUsage{}}
}

// addTranscript adds one session to the aggregate and returns its brief.
func (st *Stats) addTranscript(tr *Transcript, tools map[string]*ToolStat) Brief {
	st.Sessions++
	b := Brief{Agent: tr.Agent, ID: tr.ID, Cwd: tr.Cwd, Title: tr.Title, Events: len(tr.Events)}
	callTool := map[string]string{}
	var first, last time.Time
	for _, ev := range tr.Events {
		st.Events++
		st.ByKind[ev.Kind]++
		if !ev.TS.IsZero() {
			if first.IsZero() || ev.TS.Before(first) {
				first = ev.TS
			}
			if ev.TS.After(last) {
				last = ev.TS
			}
		}
		switch ev.Kind {
		case "user_message":
			st.UserMessages++
		case "turn_start":
			st.Turns++
		case "tool_call":
			st.ToolCalls++
			b.ToolCalls++
			callTool[ev.CallID] = ev.Tool
			t := tools[ev.Tool]
			if t == nil {
				t = &ToolStat{Name: ev.Tool}
				tools[ev.Tool] = t
			}
			t.Calls++
		case "tool_result":
			if ev.IsError {
				st.ToolErrors++
				if t := tools[callTool[ev.CallID]]; t != nil {
					t.Errors++
				}
			}
		case "token_usage":
			st.Tokens.Add(*ev.Tokens)
			b.Tokens.Add(*ev.Tokens)
			m := firstNonEmpty(ev.Model, "unknown")
			if st.TokensByModel[m] == nil {
				st.TokensByModel[m] = &TokenUsage{}
			}
			st.TokensByModel[m].Add(*ev.Tokens)
		}
	}
	// Claude has no explicit turn bounds: a turn starts with a human's reply.
	if tr.Agent == "claude" {
		for _, ev := range tr.Events {
			if ev.Kind == "user_message" {
				st.Turns++
			}
		}
	}
	if !first.IsZero() {
		b.DurationSec = last.Sub(first).Seconds()
		st.DurationSec += b.DurationSec
		if st.First.IsZero() || first.Before(st.First) {
			st.First = first
		}
		if last.After(st.Last) {
			st.Last = last
		}
	}
	return b
}

func finishTools(st *Stats, tools map[string]*ToolStat) {
	st.Tools = []ToolStat{}
	for _, t := range tools {
		st.Tools = append(st.Tools, *t)
	}
	sort.Slice(st.Tools, func(i, j int) bool {
		if st.Tools[i].Calls != st.Tools[j].Calls {
			return st.Tools[i].Calls > st.Tools[j].Calls
		}
		return st.Tools[i].Name < st.Tools[j].Name
	})
}

// pageBytes is how many bytes of transcripts one page of a period parses at most: a page
// must fit in the agent's MCP timeout. A session above it is still read, alone.
const pageBytes = 256 << 20

// compressedRatio is how many bytes a compressed Codex rollout (.jsonl.zst) counts per byte
// of its file against pageBytes, since it is parsed unpacked. zstd packed the archived
// rollouts measured on the owner's Mac 1.6–4.8 times; 4 is near the upper end, so that a
// page of them still fits in the timeout.
const compressedRatio = 4

// parseCost is how many bytes of pageBytes a session takes: its file size, unpacked for a
// compressed rollout.
func parseCost(si Info) int64 {
	if si.Compressed {
		return si.Size * compressedRatio
	}
	return si.Size
}

// StatsOf aggregates one session or a period. A page of a period reads at most f.Limit
// sessions and pageBytes of their transcripts; note says how to read the next page, if any.
func StatsOf(ctx context.Context, r Roots, in StatsIn, now time.Time) (*Stats, string, error) {
	return statsOf(ctx, r.request(), in, now, pageBytes)
}

// statsOf is StatsOf with the byte cap of a page as a parameter, for the tests.
func statsOf(ctx context.Context, r Roots, in StatsIn, now time.Time, capBytes int64) (*Stats, string, error) {
	per, err := ParsePeriod(in.From, in.To, now)
	if err != nil {
		return nil, "", err
	}
	st, tools := newStats(), map[string]*ToolStat{}
	if in.ID != "" {
		si, err := Find(ctx, r, in.Agent, in.ID, now)
		if err != nil {
			return nil, "", err
		}
		tr, _, release, err := parse(ctx, si)
		defer release()
		if err != nil {
			return nil, "", err
		}
		var skipped int
		tr.Events, skipped = inPeriod(tr.Events, per)
		if per.Set() {
			st.Coverage = &Coverage{SessionsFound: 1, SessionsRead: 1, EventsInPeriod: len(tr.Events), EventsOutOfPeriod: skipped}
			if len(tr.Events) > 0 {
				st.Coverage.SessionsInPeriod = 1
			}
		}
		st.addTranscript(tr, tools)
		finishTools(st, tools)
		return st, "", nil
	}
	f := in.Filter
	f.Offset = max(f.Offset, 0) // as List reads it
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Since == "" && in.From != "" {
		f.Since = in.From // a file changed before the window opened has no events in it
	}
	list, total, err := List(ctx, r, f, now)
	if err != nil {
		return nil, "", err
	}
	cov := &Coverage{SessionsFound: total}
	var parsed int64
	for _, si := range list {
		cost := parseCost(si)
		if cov.SessionsRead > 0 && parsed+cost > capBytes {
			break
		}
		cov.SessionsRead++
		tr, decoded, release, err := parse(ctx, si)
		parsed += decoded // what was read, unpacked, not the estimate the page was judged on (HT-381)
		if ctx.Err() != nil {
			release()
			return nil, "", ctx.Err()
		}
		if err != nil {
			cov.Unreadable++
			continue
		}
		var skipped int
		tr.Events, skipped = inPeriod(tr.Events, per)
		cov.EventsOutOfPeriod += skipped
		if per.Set() && len(tr.Events) == 0 {
			release()
			continue
		}
		cov.SessionsInPeriod++
		cov.EventsInPeriod += len(tr.Events)
		st.PerSession = append(st.PerSession, st.addTranscript(tr, tools))
		release() // the session is folded into the aggregates; its events go
	}
	if f.Offset+cov.SessionsRead < total {
		cov.NextOffset = f.Offset + cov.SessionsRead
	}
	st.Coverage = cov
	finishTools(st, tools)
	note := ""
	if cov.NextOffset > 0 {
		note = fmt.Sprintf("sessions %d–%d of %d read; next page: offset=%d", f.Offset+1, cov.NextOffset, total, cov.NextOffset)
	}
	return st, note, nil
}
