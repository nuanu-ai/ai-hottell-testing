package coach

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// maxEvidence is how many pieces of evidence a finding carries at most: the last placed.
// The builders keep evidence oldest to newest, so these are the newest.
const maxEvidence = 3

// maxEvidenceTextRunes is how long the text of a piece of evidence may be.
const maxEvidenceTextRunes = 160

// What is read: only the fields of the datasets the findings need (ui/CONTRACT.md).

type dsEvidence struct {
	SID  string `json:"sid"`
	Line int    `json:"line"`
	At   string `json:"at"`
	Text string `json:"text"`
}

type dsFinding struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Kind      string       `json:"kind"`
	PatternID string       `json:"pattern_id"`
	Sev       string       `json:"sev"`
	Readiness string       `json:"readiness"`
	Decision  string       `json:"decision"`
	Execution string       `json:"execution"`
	Scope     string       `json:"scope"` // "collection" in the live dataset: health of the data collection
	Sessions  []string     `json:"sessions"`
	Ev        []dsEvidence `json:"ev"`
}

type dsFriction struct {
	Key      string       `json:"key"`
	Name     string       `json:"name"`
	Sev      string       `json:"sev"`
	Sessions []string     `json:"sessions"`
	Evidence []dsEvidence `json:"evidence"`
}

type dsSkill struct {
	Name        string       `json:"name"`
	State       string       `json:"state"`
	Activations int          `json:"activations"`
	Evidence    []dsEvidence `json:"evidence"`
}

type dsSession struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type dashDataset struct {
	GeneratedAt string `json:"generated_at"`
	Window      struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"window"`
	Findings []dsFinding  `json:"findings"`
	Friction []dsFriction `json:"friction"`
	Skills   struct {
		Rows []dsSkill `json:"rows"`
	} `json:"skills"`
	Sessions []dsSession `json:"sessions"`
}

type regStatus struct {
	Status string `json:"status"`
}

type regProposal struct {
	ProposalID string    `json:"proposal_id"`
	PatternID  string    `json:"pattern_id"`
	ChangeType string    `json:"change_type"`
	Action     string    `json:"action"`
	Decision   regStatus `json:"decision"`
	Execution  regStatus `json:"execution"`
	Readiness  regStatus `json:"readiness"`
	Sources    []struct {
		SessionID string   `json:"session_id"`
		Evidence  []string `json:"evidence"` // L1157: rollout lines, without a time
	} `json:"sources"`
}

type proposalRegistry struct {
	Proposals []regProposal `json:"proposals"`
}

// finding is a registry proposal missing from the reviewed dataset, shaped as its findings.
func (p regProposal) finding() dsFinding {
	f := dsFinding{
		ID: p.ProposalID, Title: p.Action, Kind: p.ChangeType, PatternID: p.PatternID,
		Readiness: p.Readiness.Status, Decision: p.Decision.Status, Execution: p.Execution.Status,
	}
	for _, s := range p.Sources {
		f.Sessions = append(f.Sessions, s.SessionID)
		for _, l := range s.Evidence {
			if n, err := strconv.Atoi(strings.TrimPrefix(l, "L")); err == nil {
				f.Ev = append(f.Ev, dsEvidence{SID: s.SessionID, Line: n})
			}
		}
	}
	return f
}

// What is returned.

// FindingRef is one piece of evidence of a finding.
type FindingRef struct {
	Session string `json:"session"`
	At      string `json:"at,omitempty"`
	Line    int    `json:"line,omitempty"`
	// LineOf says what Line counts: live_timeline is a line of the live dashboard's timeline,
	// rollout a line of the rollout. Neither is the seq of session_read.
	LineOf string `json:"line_of"`
	Text   string `json:"text,omitempty"`
}

// FindingBrief is a finding in short.
type FindingBrief struct {
	ID        string `json:"id"`
	Source    string `json:"source"` // live or reviewed
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Pattern   string `json:"pattern,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Readiness string `json:"readiness,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Execution string `json:"execution,omitempty"`
	// Collection marks the health of the data collection, not the person's work.
	Collection bool     `json:"collection,omitempty"`
	Sessions   []string `json:"sessions"`
	// Evidence is the newest maxEvidence of the evidence placed, oldest first.
	Evidence []FindingRef `json:"evidence"`
	// EvidenceTotal is how much evidence was placed, before the cut to maxEvidence.
	EvidenceTotal int `json:"evidence_total"`
}

// ServiceSession is a session that is not the person's work: system or automation.
type ServiceSession struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// SourceState says whether a dataset was read: ok, missing or broken.
type SourceState struct {
	Name        string `json:"name"` // live, reviewed or registry
	Path        string `json:"path"`
	State       string `json:"state"`
	GeneratedAt string `json:"generated_at,omitempty"`
	WindowFrom  string `json:"window_from,omitempty"`
	WindowTo    string `json:"window_to,omitempty"`
	Error       string `json:"error,omitempty"`
}

// FindingsIn selects the findings.
type FindingsIn struct {
	Since  string `json:"since,omitempty" jsonschema:"start of the period over the time of the evidence: YYYY-MM-DD, RFC3339 or a duration back (168h)"`
	Until  string `json:"until,omitempty" jsonschema:"end of the period: YYYY-MM-DD (inclusive) or RFC3339"`
	Source string `json:"source,omitempty" jsonschema:"live, reviewed or all (all by default)"`
	ID     string `json:"id,omitempty" jsonschema:"one finding: live:…, p2:… or friction:<key>"`
}

// FindingsOut is the findings of the period and what they were read from.
type FindingsOut struct {
	DataDir  string         `json:"data_dir"`
	Sources  []SourceState  `json:"sources"`
	Findings []FindingBrief `json:"findings"`
	// Undated are findings none of whose evidence has a time, so the period cannot hold
	// them; listed when one of their sessions meets the period.
	Undated         []FindingBrief   `json:"undated"`
	Skills          []SkillBrief     `json:"skills,omitempty"`
	ServiceSessions []ServiceSession `json:"service_sessions,omitempty"`
}

// SkillBrief is a skill used inside the period.
type SkillBrief struct {
	Name             string `json:"name"`
	State            string `json:"state,omitempty"`
	Activations      int    `json:"activations"`        // over the dataset's window, not the period
	EvidenceInPeriod int    `json:"evidence_in_period"` // activations seen inside the period
	Sessions         int    `json:"sessions"`           // sessions with such evidence
}

type span struct{ from, to time.Time }

// placement is where a finding goes for the period.
type placement int

const (
	placeOut placement = iota
	placeIn
	placeUndated
)

// Findings reads the live dataset (dataDir/ui-live), the reviewed one (dataDir/ui) and the
// proposal registry (dataDir/reports/v2) and returns the findings of the period. A missing
// or broken file is a source state, not an error, and gives no findings.
func Findings(dataDir string, in FindingsIn, now time.Time) (FindingsOut, error) {
	src := in.Source
	if src == "" {
		src = "all"
	}
	if src != "live" && src != "reviewed" && src != "all" {
		return FindingsOut{}, fmt.Errorf("source: live, reviewed or all, not %q", in.Source)
	}
	per, err := sessions.ParsePeriod(in.Since, in.Until, now)
	if err != nil {
		return FindingsOut{}, fmt.Errorf("period: %w", err)
	}
	out := FindingsOut{DataDir: dataDir, Sources: []SourceState{}, Findings: []FindingBrief{}, Undated: []FindingBrief{}}
	add := func(f dsFinding, source, lineOf string, spans map[string]span) {
		if in.ID != "" && f.ID != in.ID {
			return
		}
		switch b, where := brief(f, source, lineOf, spans, per); where {
		case placeIn:
			out.Findings = append(out.Findings, b)
		case placeUndated:
			out.Undated = append(out.Undated, b)
		case placeOut:
		}
	}
	if src != "reviewed" {
		ds, st := loadDataset("live", filepath.Join(dataDir, "ui-live", "dataset.json"))
		out.Sources = append(out.Sources, st)
		spans := sessionSpans(ds)
		for _, f := range ds.Findings {
			add(f, "live", "live_timeline", spans)
		}
		for _, fr := range ds.Friction {
			add(dsFinding{
				ID: "friction:" + fr.Key, Title: fr.Name, Kind: "friction", PatternID: fr.Key, Sev: fr.Sev,
				Readiness: "hypothesis", Sessions: fr.Sessions, Ev: fr.Evidence,
			}, "live", "live_timeline", spans)
		}
		for _, r := range ds.Skills.Rows {
			n, sids := 0, map[string]bool{}
			for _, e := range r.Evidence {
				if per.Has(parseTime(e.At)) {
					n++
					sids[e.SID] = true
				}
			}
			if r.Activations > 0 && (n > 0 || !per.Set()) {
				out.Skills = append(out.Skills, SkillBrief{
					Name: r.Name, State: r.State, Activations: r.Activations,
					EvidenceInPeriod: n, Sessions: len(sids),
				})
			}
		}
		for _, s := range ds.Sessions {
			if (s.Kind == "system" || s.Kind == "automation") && per.Overlaps(parseTime(s.Start), parseTime(s.End)) {
				out.ServiceSessions = append(out.ServiceSessions, ServiceSession{ID: s.ID, Kind: s.Kind})
			}
		}
	}
	if src != "live" {
		ds, st := loadDataset("reviewed", filepath.Join(dataDir, "ui", "dataset.json"))
		out.Sources = append(out.Sources, st)
		reg, regSt := readJSONSource[proposalRegistry]("registry", filepath.Join(dataDir, "reports", "v2", "proposals.json"))
		out.Sources = append(out.Sources, regSt)
		spans := sessionSpans(ds)
		byID := map[string]regProposal{}
		if regSt.State == "ok" {
			for _, p := range reg.Proposals {
				byID[p.ProposalID] = p
			}
		}
		seen := map[string]bool{}
		for _, f := range ds.Findings {
			if p, ok := byID[f.ID]; ok { // the registry keeps the decision and execution statuses
				f.Decision = firstSet(p.Decision.Status, f.Decision)
				f.Execution = firstSet(p.Execution.Status, f.Execution)
			}
			seen[f.ID] = true
			add(f, "reviewed", "rollout", spans)
		}
		for _, p := range reg.Proposals {
			if !seen[p.ProposalID] {
				add(p.finding(), "reviewed", "rollout", spans)
			}
		}
	}
	return out, nil
}

// brief places a finding: in the period only with evidence timed inside the window; apart,
// in Undated, when none of its evidence has a time and one of its sessions meets the
// window. A session that merely spans the window proves nothing: its evidence may be
// months old.
func brief(f dsFinding, source, lineOf string, spans map[string]span, per sessions.Period) (FindingBrief, placement) {
	b := FindingBrief{
		ID: f.ID, Source: source, Title: f.Title, Kind: f.Kind, Pattern: f.PatternID, Severity: f.Sev,
		Readiness: f.Readiness, Decision: f.Decision, Execution: f.Execution, Collection: f.Scope == "collection",
		Sessions: []string{}, Evidence: []FindingRef{},
	}
	seen := map[string]bool{}
	addSession := func(sid string) {
		if !seen[sid] {
			seen[sid] = true
			b.Sessions = append(b.Sessions, sid)
		}
	}
	addEvidence := func(e dsEvidence) {
		addSession(e.SID)
		b.EvidenceTotal++
		b.Evidence = append(b.Evidence, FindingRef{
			Session: e.SID, At: e.At, Line: e.Line, LineOf: lineOf,
			Text: clipRunes(e.Text, maxEvidenceTextRunes),
		})
		if len(b.Evidence) > maxEvidence {
			b.Evidence = b.Evidence[1:]
		}
	}
	addAll := func() {
		for _, e := range f.Ev {
			addEvidence(e)
		}
		for _, sid := range f.Sessions {
			addSession(sid)
		}
	}
	if !per.Set() {
		addAll()
		return b, placeIn
	}
	dated := false
	for _, e := range f.Ev {
		t := parseTime(e.At)
		dated = dated || !t.IsZero()
		if per.Has(t) { // false for an evidence without a time
			addEvidence(e)
		}
	}
	switch {
	case b.EvidenceTotal > 0:
		return b, placeIn
	case dated:
		return b, placeOut
	}
	for _, sid := range f.Sessions {
		if sp := spans[sid]; per.Overlaps(sp.from, sp.to) {
			addAll()
			return b, placeUndated
		}
	}
	return b, placeOut
}

func sessionSpans(ds dashDataset) map[string]span {
	m := make(map[string]span, len(ds.Sessions))
	for _, s := range ds.Sessions {
		m[s.ID] = span{parseTime(s.Start), parseTime(s.End)}
	}
	return m
}

// firstSet is the registry's status when it has one, else the dataset's.
func firstSet(registry, dataset string) string {
	if registry != "" {
		return registry
	}
	return dataset
}

// readJSONSource reads the file of a source. A missing file is the state missing, an
// unreadable or broken one the state broken; either gives the zero value, never the part
// JSON filled before the error.
func readJSONSource[T any](name, path string) (T, SourceState) {
	var zero, v T
	st := SourceState{Name: name, Path: path, State: "ok"}
	b, err := os.ReadFile(path) //nolint:gosec // a file of the dashboard's own data directory
	switch {
	case errors.Is(err, fs.ErrNotExist):
		st.State = "missing"
		return zero, st
	case err != nil:
		st.State, st.Error = "broken", err.Error()
		return zero, st
	}
	if err := json.Unmarshal(b, &v); err != nil {
		st.State, st.Error = "broken", err.Error()
		return zero, st
	}
	return v, st
}

func loadDataset(name, path string) (dashDataset, SourceState) {
	ds, st := readJSONSource[dashDataset](name, path)
	if st.State == "ok" {
		st.GeneratedAt, st.WindowFrom, st.WindowTo = ds.GeneratedAt, ds.Window.From, ds.Window.To
	}
	return ds, st
}

// parseTime reads an RFC3339 time of the datasets; anything else is no time.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// clipRunes cuts by characters, not bytes: the texts are in Cyrillic.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
