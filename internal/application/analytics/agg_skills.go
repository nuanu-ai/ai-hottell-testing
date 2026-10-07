package analytics

import (
	"strconv"
)

// The states of a row of the skills aggregate.
const (
	// SkillStateUsed: the skill was opened.
	SkillStateUsed = "used"
	// SkillStateUnused: the row of the skills of the snapshot never opened.
	SkillStateUnused = "unused"
)

// GapNoSkillSnapshot is the gap of a dataset whose sessions sent no skill snapshot.
const GapNoSkillSnapshot = "Снимок доступных skills не пришёл ни от одной сессии: сколько skills доступно и какие не открывались — неизвестно."

// skillEvidenceTextMax is the most characters of the text of a skill's evidence.
const skillEvidenceTextMax = 140

// Skills is the skills block of the dataset.
type Skills struct {
	// Available counts the skills of the snapshots and the skills opened together; nil without
	// a snapshot.
	Available *int `json:"available"`
	// SnapshotComplete is false when a snapshot left the skills of plugins out; nil without one.
	SnapshotComplete *bool      `json:"snapshot_complete"`
	Rows             []SkillRow `json:"rows"`
	// Gantt is the timeline of the busiest session (BuildGantt), absent when none has a skill
	// or a subagent.
	Gantt *Gantt `json:"gantt,omitempty"`
}

// SkillRow is one skill of the skills block, or the last row of the skills never opened.
type SkillRow struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	// Activations, Sessions and BySession count the skill's activations; BySession adds up to
	// Activations.
	Activations int            `json:"activations"`
	Sessions    []string       `json:"sessions"`
	BySession   map[string]int `json:"by_session"`
	// FirstLine is the timeline line of the first activation, nil without one.
	FirstLine *int `json:"first_line"`
	// ToCodeMin and CorrectionsAfter are not measured live.
	ToCodeMin        *float64 `json:"to_code_min"`
	CorrectionsAfter *int     `json:"corrections_after"`
	Subagents        int      `json:"subagents"`
	// SizeKtok is the skill's size from the latest read of its whole file, nil without one.
	SizeKtok *float64   `json:"size_ktok"`
	State    string     `json:"state"`
	Evidence []Evidence `json:"evidence"`
}

// AggregateSkills counts the skill activations of the sessions, as the colleague's
// aggregate_skills: a row per skill in the order first opened, with the sessions, the line and
// the evidence of each activation. The skills of the sessions' snapshots that were never opened
// make one last row, and with the opened ones give available; without a snapshot available and
// snapshot_complete are nil.
func AggregateSkills(sessions []*BuiltSession) Skills {
	available := map[string]bool{}
	complete, snapped := true, false
	for _, b := range sessions {
		if b.Snapshot == nil {
			continue
		}
		snapped = true
		complete = complete && b.Snapshot.Complete
		for _, it := range b.Snapshot.Items {
			if it.Name != "" {
				available[it.Name] = true
			}
		}
	}
	var rows []*SkillRow
	byName := map[string]*SkillRow{}
	for _, b := range sessions {
		sid := b.Key.SessionID
		for _, a := range b.Skills {
			r, ok := byName[a.Name]
			if !ok {
				r = &SkillRow{
					Name: a.Name, Source: a.Source, Sessions: []string{}, BySession: map[string]int{},
					State: SkillStateUsed, Evidence: []Evidence{},
				}
				byName[a.Name] = r
				rows = append(rows, r)
			}
			r.Activations++
			if r.BySession[sid] == 0 {
				r.Sessions = append(r.Sessions, sid)
			}
			r.BySession[sid]++
			line := 0
			ev := Evidence{SID: sid, At: ISO(a.At)}
			if i := activationCall(b.Calls.Calls, a); i >= 0 {
				c := &b.Calls.Calls[i]
				line = b.Timeline.Line(RefCall, i)
				ev.Text = ToolSummary(c.Tool, c.Input, skillEvidenceTextMax)
			}
			ev.Line = line
			if r.FirstLine == nil && line > 0 {
				r.FirstLine = &line
			}
			if a.SizeKtok != nil {
				r.SizeKtok = a.SizeKtok
			}
			r.Evidence = append(r.Evidence, ev)
		}
	}
	out := make([]SkillRow, 0, len(rows)+1)
	for _, r := range rows {
		out = append(out, *r)
	}
	unused := 0
	for name := range available {
		if byName[name] == nil {
			unused++
		}
	}
	if unused > 0 {
		out = append(out, SkillRow{
			Name: "…", Source: "остальные " + strconv.Itoa(unused) + " skills — 0 активаций", Sessions: []string{},
			BySession: map[string]int{}, State: SkillStateUnused, Evidence: []Evidence{},
		})
	}
	s := Skills{Rows: out, Gantt: BuildGantt(sessions)}
	if snapped {
		n := len(available) + len(byName) - (len(available) - unused)
		s.Available, s.SnapshotComplete = &n, &complete
	}
	return s
}

// activationCall is the index of the call of an activation among calls, -1 when none matches.
func activationCall(calls []Call, a SkillActivation) int {
	for i := range calls {
		if calls[i].ToolUseID == a.ToolUseID && calls[i].At.Equal(a.At) {
			return i
		}
	}
	return -1
}

// SkillsGap is GapNoSkillSnapshot when the skills block has no snapshot.
func SkillsGap(s Skills) (string, bool) {
	if s.Available != nil {
		return "", false
	}
	return GapNoSkillSnapshot, true
}
