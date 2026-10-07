package analytics

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The rows of the gantt besides the skills.
const (
	GanttSubagents = "субагенты"
	GanttMCP       = "MCP"
	GanttNoSkill   = "без skills"
)

// The kinds of a gantt segment.
const (
	GanttSegSkill    = "skill"
	GanttSegSubagent = "subagent"
	GanttSegMCP      = "mcp"
	GanttSegNoSkill  = "noskill"
)

const (
	// mcpMergeGap is the pause under which two MCP calls make one segment.
	mcpMergeGap = 120 * time.Second
	// ganttLabelMax and ganttMarkMax are the most characters of a label and of a mark's prompt.
	ganttLabelMax = 60
	ganttMarkMax  = 28
)

// Gantt is the skills, subagents and MCP of one session over time, for the Skills screen.
type Gantt struct {
	SID   string `json:"sid"`
	Title string `json:"title"`
	// From and To bound the turns that hold an activation.
	From  string      `json:"from"`
	To    string      `json:"to"`
	Rows  []GanttRow  `json:"rows"`
	Marks []GanttMark `json:"marks"`
}

// GanttRow is one row of the gantt: a skill, the subagents, MCP or the turns without a skill.
type GanttRow struct {
	Name string     `json:"name"`
	Segs []GanttSeg `json:"segs"`
}

// GanttSeg is one segment of a row.
type GanttSeg struct {
	From  string `json:"from"`
	To    string `json:"to"`
	C     string `json:"c"`
	Label string `json:"label"`
}

// GanttMark is one of your prompts in the gantt, with its timeline line.
type GanttMark struct {
	At   string `json:"at"`
	Text string `json:"text"`
	Line int    `json:"line"`
}

// BuildGantt lays out the session with the most skill activations, subagents and Agent or
// collaboration.spawn calls, as the colleague's _gantt: a skill lasts from its activation to the
// end of its turn, a subagent from its start to its stop (or the Agent call to its Post), MCP
// calls less than two minutes apart merge, and the turns' stretches without a skill of at
// least a second make the last row. Your prompts are the marks. Nil when no session has an
// activation or a subagent.
func BuildGantt(sessions []*BuiltSession) *Gantt {
	var best *BuiltSession
	bestScore := 0
	for _, b := range sessions {
		if s := ganttScore(b); s > bestScore {
			best, bestScore = b, s
		}
	}
	if best == nil {
		return nil
	}
	return ganttOf(best)
}

// ganttScore counts a session's skill activations, subagent starts and stops, and the Agent and
// collaboration.spawn calls.
func ganttScore(b *BuiltSession) int {
	n := len(b.Skills) + countEvents(b.Events, "SubagentStart") + countEvents(b.Events, "SubagentStop")
	for i := range b.Calls.Calls {
		c := &b.Calls.Calls[i]
		if c.Tool == "Agent" || strings.HasPrefix(c.Name, "collaboration.spawn") {
			n++
		}
	}
	return n
}

func ganttOf(b *BuiltSession) *Gantt {
	calls, turns := b.Calls.Calls, b.Turns.List
	var acts []time.Time
	for _, a := range b.Skills {
		acts = append(acts, a.At)
	}
	var subStart, subStop []telemetry.HookEvent
	for _, e := range b.Events {
		switch e.Event {
		case "SubagentStart":
			subStart = append(subStart, e)
			acts = append(acts, e.Time)
		case "SubagentStop":
			subStop = append(subStop, e)
			acts = append(acts, e.Time)
		}
	}
	for i := range calls {
		if calls[i].Tool == "Agent" || strings.HasPrefix(calls[i].Name, "collaboration.") {
			acts = append(acts, calls[i].At)
		}
	}
	var lo, hi time.Time
	for _, t := range turns {
		if !slices.ContainsFunc(acts, func(x time.Time) bool { return !x.Before(t.Start) && !x.After(t.End) }) {
			continue
		}
		if lo.IsZero() || t.Start.Before(lo) {
			lo = t.Start
		}
		if t.End.After(hi) {
			hi = t.End
		}
	}
	if lo.IsZero() {
		return nil
	}
	inside := func(x time.Time) bool { return !x.IsZero() && !x.Before(lo) && !x.After(hi) }
	turnEnd := func(at time.Time) time.Time {
		i := sort.Search(len(turns), func(j int) bool { return turns[j].Start.After(at) }) - 1
		if i < 0 {
			return at
		}
		return turns[i].End
	}
	upTo := func(t time.Time) time.Time {
		if t.After(hi) {
			return hi
		}
		return t
	}

	var rows []GanttRow
	var skilled []TimeSpan
	var order []string
	bySkill := map[string][]GanttSeg{}
	for _, a := range b.Skills {
		if !inside(a.At) {
			continue
		}
		end := upTo(turnEnd(a.At))
		if _, ok := bySkill[a.Name]; !ok {
			order = append(order, a.Name)
		}
		bySkill[a.Name] = append(bySkill[a.Name], GanttSeg{
			From: ISO(a.At), To: ISO(end), C: GanttSegSkill, Label: Clean(a.Name, ganttLabelMax),
		})
		skilled = append(skilled, TimeSpan{From: a.At, To: end})
	}
	for _, name := range order {
		rows = append(rows, GanttRow{Name: Clean(name, ganttLabelMax), Segs: bySkill[name]})
	}

	slices.SortStableFunc(subStop, func(x, y telemetry.HookEvent) int { return x.Time.Compare(y.Time) })
	var sub []GanttSeg
	for _, r := range subStart {
		if !inside(r.Time) {
			continue
		}
		end := turnEnd(r.Time)
		for _, x := range subStop {
			if !x.Time.Before(r.Time) && (x.AgentID == r.AgentID || r.AgentID == "") {
				end = x.Time
				break
			}
		}
		label := r.AgentType
		if label == "" {
			label = "субагент"
		}
		sub = append(sub, GanttSeg{From: ISO(r.Time), To: ISO(upTo(end)), C: GanttSegSubagent, Label: Clean(label, ganttLabelMax)})
	}
	var mcp []TimeSpan
	for i := range calls {
		c := &calls[i]
		if c.Tool == "Agent" && inside(c.At) {
			end := c.PostAt
			if end.IsZero() {
				end = turnEnd(c.At)
			}
			sub = append(sub, GanttSeg{
				From: ISO(c.At), To: ISO(upTo(end)), C: GanttSegSubagent,
				Label: Clean(ToolSummary("Agent", c.Input, ganttLabelMax), ganttLabelMax),
			})
		}
		if c.MCP != "" && inside(c.At) {
			end := c.PostAt
			if end.IsZero() {
				end = c.At
			}
			mcp = append(mcp, TimeSpan{From: c.At, To: end, Label: c.MCP})
		}
	}
	if len(sub) > 0 {
		rows = append(rows, GanttRow{Name: GanttSubagents, Segs: sub})
	}
	if len(mcp) > 0 {
		var segs []GanttSeg
		for _, m := range MergeSegments(mcp, mcpMergeGap) {
			servers := slices.Compact(slices.Sorted(slices.Values(m.Labels)))
			segs = append(segs, GanttSeg{
				From: ISO(m.From), To: ISO(m.To), C: GanttSegMCP,
				Label: strconv.Itoa(len(m.Labels)) + " выз. · " + strings.Join(servers, ", "),
			})
		}
		rows = append(rows, GanttRow{Name: GanttMCP, Segs: segs})
	}
	if segs := noSkillSegs(turns, skilled, lo, hi); len(segs) > 0 {
		rows = append(rows, GanttRow{Name: GanttNoSkill, Segs: segs})
	}

	marks := []GanttMark{}
	for i := range b.Prompts {
		p := &b.Prompts[i]
		if inside(p.At) && p.Person() {
			marks = append(marks, GanttMark{
				At: ISO(p.At), Text: "«" + Clean(p.Text, ganttMarkMax) + "»", Line: b.Timeline.Line(RefPrompt, i),
			})
		}
	}
	project := b.Project
	if project == "" {
		project = "?"
	}
	return &Gantt{
		SID: b.Key.SessionID, Title: ShortID(b.Key.SessionID) + " · " + b.Key.Agent + " · " + project,
		From: ISO(lo), To: ISO(hi), Rows: rows, Marks: marks,
	}
}

// noSkillSegs are the stretches of the turns within [lo, hi] that no skill covers, a second long
// or more.
func noSkillSegs(turns []*Turn, skilled []TimeSpan, lo, hi time.Time) []GanttSeg {
	slices.SortFunc(skilled, func(x, y TimeSpan) int {
		if c := x.From.Compare(y.From); c != 0 {
			return c
		}
		return x.To.Compare(y.To)
	})
	var segs []GanttSeg
	add := func(a, b time.Time) {
		if b.Sub(a) >= time.Second {
			segs = append(segs, GanttSeg{From: ISO(a), To: ISO(b), C: GanttSegNoSkill, Label: "ход без skill"})
		}
	}
	for _, t := range turns {
		a, b := t.Start, t.End
		if a.Before(lo) {
			a = lo
		}
		if b.After(hi) {
			b = hi
		}
		if !b.After(a) {
			continue
		}
		x := a
		for _, s := range skilled {
			if !s.To.After(x) || !s.From.Before(b) {
				continue
			}
			if s.From.After(x) {
				add(x, s.From)
			}
			if s.To.After(x) {
				x = s.To
			}
		}
		if x.Before(b) {
			add(x, b)
		}
	}
	return segs
}
