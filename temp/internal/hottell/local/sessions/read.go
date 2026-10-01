package sessions

import "time"

// ReadIn selects one session's events, page by page.
type ReadIn struct {
	Agent   string   `json:"agent,omitempty" jsonschema:"claude or codex; helps when the id is ambiguous"`
	ID      string   `json:"id" jsonschema:"session id, a unique prefix of it, or the path of the transcript"`
	Offset  int      `json:"offset,omitempty" jsonschema:"first event (seq) of the page; 0 by default"`
	Limit   int      `json:"limit,omitempty" jsonschema:"events per page; 100 by default"`
	Kinds   []string `json:"kinds,omitempty" jsonschema:"only these kinds: user_message, assistant_message, reasoning, tool_call, tool_result, system, attachment, token_usage, turn_start, turn_end, compact"`
	MaxText int      `json:"max_text,omitempty" jsonschema:"cut texts to this many bytes; 0 means 2000, -1 means no cut"`
	From    string   `json:"from,omitempty" jsonschema:"only events at or after: YYYY-MM-DD, RFC3339 or a duration back (168h)"`
	To      string   `json:"to,omitempty" jsonschema:"only events before: YYYY-MM-DD (inclusive) or RFC3339"`
}

// ReadOut is one page. The counters cover the whole session, not the page.
type ReadOut struct {
	Session     Info        `json:"session"`
	Meta        *Transcript `json:"meta"`
	Total       int         `json:"total_events"`
	Matched     int         `json:"matched_events"` // of the kinds asked, inside the window
	OutOfPeriod int         `json:"out_of_period"`  // of the kinds asked, outside the window
	Next        int         `json:"next_offset,omitempty"`
	Events      []Event     `json:"events"`
}

// Read returns one page of a session's events within the window.
func Read(r Roots, in ReadIn, now time.Time) (ReadOut, error) {
	per, err := ParsePeriod(in.From, in.To, now)
	if err != nil {
		return ReadOut{}, err
	}
	si, err := Find(r, in.Agent, in.ID, now)
	if err != nil {
		return ReadOut{}, err
	}
	tr, err := Parse(si)
	if err != nil {
		return ReadOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	maxText := in.MaxText
	if maxText == 0 {
		maxText = 2000
	}
	want := map[string]bool{}
	for _, k := range in.Kinds {
		want[k] = true
	}
	out := ReadOut{Session: si, Meta: tr, Total: len(tr.Events), Events: []Event{}}
	for _, ev := range tr.Events {
		if len(want) > 0 && !want[ev.Kind] {
			continue
		}
		if !per.Has(ev.TS) {
			out.OutOfPeriod++
			continue
		}
		out.Matched++
		if ev.Seq < in.Offset {
			continue
		}
		if len(out.Events) == limit { // the page is full; keep counting
			if out.Next == 0 {
				out.Next = ev.Seq
			}
			continue
		}
		if maxText > 0 {
			ev.Text = Truncate(ev.Text, maxText)
		}
		out.Events = append(out.Events, ev)
	}
	return out, nil
}
