package analytics

import (
	"cmp"
	"crypto/sha1" //nolint:gosec // an id digest, as the colleague's live.py; not a security use
	"encoding/hex"
	"slices"
	"time"
)

// Severity is how bad a finding is: bad, warn or info.
type Severity string

// The severities.
const (
	SevBad  Severity = "bad"
	SevWarn Severity = "warn"
	SevInfo Severity = "info"
)

// sevRank orders severities, bad first; an unknown one ranks after info.
func sevRank(s Severity) int {
	switch s {
	case SevBad:
		return 0
	case SevWarn:
		return 1
	case SevInfo:
		return 2
	}
	return 3
}

// Readiness is how far a finding's change is prepared.
type Readiness string

// The readiness values.
const (
	ReadinessHypothesis Readiness = "hypothesis"
	ReadinessNeedsSpec  Readiness = "needs_spec"
	ReadinessPrepared   Readiness = "prepared"
)

// Valid reports whether r is one of the readiness values.
func (r Readiness) Valid() bool {
	return r == ReadinessHypothesis || r == ReadinessNeedsSpec || r == ReadinessPrepared
}

// Rank orders readiness, hypothesis lowest, as the colleague's _READY_RANK; an unknown
// value ranks as hypothesis.
func (r Readiness) Rank() int {
	switch r {
	case ReadinessNeedsSpec:
		return 1
	case ReadinessPrepared:
		return 2
	}
	return 0
}

// Decision is what the person decided about a finding.
type Decision string

// The decision values.
const (
	DecisionNotRequested      Decision = "not_requested"
	DecisionAccepted          Decision = "accepted"
	DecisionRejected          Decision = "rejected"
	DecisionRevisionRequested Decision = "revision_requested"
)

// Valid reports whether d is one of the decision values.
func (d Decision) Valid() bool {
	return d == DecisionNotRequested || d == DecisionAccepted || d == DecisionRejected || d == DecisionRevisionRequested
}

// Execution is whether a finding's change was applied.
type Execution string

// The execution values.
const (
	ExecutionNotApplied Execution = "not_applied"
	ExecutionApplied    Execution = "applied"
)

// Valid reports whether e is one of the execution values.
func (e Execution) Valid() bool { return e == ExecutionNotApplied || e == ExecutionApplied }

// Effect is what an applied change did.
type Effect string

// The effect values.
const (
	EffectNotMeasured      Effect = "not_measured"
	EffectHelped           Effect = "helped"
	EffectNoEffect         Effect = "no_effect"
	EffectWorse            Effect = "worse"
	EffectInsufficientData Effect = "insufficient_data"
)

// Valid reports whether e is one of the effect values.
func (e Effect) Valid() bool {
	switch e {
	case EffectNotMeasured, EffectHelped, EffectNoEffect, EffectWorse, EffectInsufficientData:
		return true
	}
	return false
}

// Kind is the way a finding's change is made.
type Kind string

// The kinds.
const (
	KindPersonalization Kind = "personalization"
	KindProjectRule     Kind = "project_rule"
	KindSkill           Kind = "skill"
	KindHook            Kind = "hook"
	KindScript          Kind = "script"
	KindAutomation      Kind = "automation"
	KindDiagnostic      Kind = "diagnostic"
	KindHabit           Kind = "habit"
)

// Kinds lists every kind.
func Kinds() []Kind {
	return []Kind{
		KindPersonalization, KindProjectRule, KindSkill, KindHook, KindScript, KindAutomation, KindDiagnostic, KindHabit,
	}
}

// Scope is work for a finding about the person's work, collection for one about the health
// of data collection.
type Scope string

// The scopes.
const (
	ScopeWork       Scope = "work"
	ScopeCollection Scope = "collection"
)

// MaxEvidence is how many evidence lines a finding keeps: the newest.
const MaxEvidence = 8

// Evidence is one line a finding rests on. At is an ISO time or "" when unknown.
type Evidence struct {
	SID  string `json:"sid"`
	Line int    `json:"line"`
	At   string `json:"at"`
	Text string `json:"text,omitempty"`
}

// Impact is a finding's effect as shown: a value and what it is per.
type Impact struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Finding is a "what to fix" card of the dashboard contract (docs/ai-hottell/ui/CONTRACT.md),
// the detector (live) part of the colleague's conclusions.py. The Deep fields and the merge of
// cards across sessions are added by the Deep stage.
type Finding struct {
	ID              string             `json:"id"`
	Sev             Severity           `json:"sev"`
	Title           string             `json:"title"`
	What            string             `json:"what"`
	Ev              []Evidence         `json:"ev"`
	Impact          *Impact            `json:"impact"`
	ImpactBySession map[string]float64 `json:"impact_by_session"`
	ImpactUnit      string             `json:"impact_unit,omitempty"`
	Where           string             `json:"where,omitempty"`
	Snip            string             `json:"snip,omitempty"`
	Kind            Kind               `json:"kind"`
	PatternID       string             `json:"pattern_id,omitempty"`
	Scope           Scope              `json:"scope"`
	Readiness       Readiness          `json:"readiness"`
	Decision        Decision           `json:"decision"`
	Execution       Execution          `json:"execution"`
	Effect          Effect             `json:"effect"`
	Source          string             `json:"source"`
	Lang            string             `json:"lang"`
	Sessions        []string           `json:"sessions"`
	// Episodes is how many episodes the card counts; the Topics screen orders cards of one
	// severity by it, most first.
	Episodes          int      `json:"episodes"`
	Cause             *string  `json:"cause"`
	AlternativeCauses []string `json:"alternative_causes"`
	Preconditions     []string `json:"preconditions"`
	Exceptions        []string `json:"exceptions"`
	Verification      *string  `json:"verification"`
	Rollback          *string  `json:"rollback"`
	ExpectedEffect    *string  `json:"expected_effect"`
}

// FindingID is the card id for a detector key: "live:" and the first 20 hex digits of the
// key's SHA-1, as the colleague's _finding, so the same key gives the same id on every build.
func FindingID(key string) string {
	sum := sha1.Sum([]byte(key)) //nolint:gosec // see the import
	return "live:" + hex.EncodeToString(sum[:])[:20]
}

// NewFinding builds a detector card with the defaults of the colleague's _finding: readiness
// hypothesis, nothing decided, applied or measured, kind diagnostic, scope work. ev keeps
// its MaxEvidence newest lines in time order; an evidence line without a time counts as the
// oldest.
func NewFinding(key string, sev Severity, title, what string, ev []Evidence, sessions []string) Finding {
	return Finding{
		ID:                FindingID(key),
		Sev:               sev,
		Title:             title,
		What:              what,
		Ev:                nonNil(Newest(ev, MaxEvidence, evidenceAt)),
		Kind:              KindDiagnostic,
		Scope:             ScopeWork,
		Readiness:         ReadinessHypothesis,
		Decision:          DecisionNotRequested,
		Execution:         ExecutionNotApplied,
		Effect:            EffectNotMeasured,
		Source:            "detector",
		Lang:              "ru",
		Sessions:          nonNil(sessions),
		AlternativeCauses: []string{},
		Preconditions:     []string{},
		Exceptions:        []string{},
	}
}

// evidenceAt reads an evidence time; one missing or unreadable is the zero time.
func evidenceAt(e Evidence) time.Time {
	t, err := time.Parse(time.RFC3339Nano, e.At)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// SortFindings orders cards as the Topics screen of dashboard v5.1: bad, warn, info, and
// within one severity by Episodes, most first; equal cards keep their order.
func SortFindings(fs []Finding) {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(sevRank(a.Sev), sevRank(b.Sev)), cmp.Compare(b.Episodes, a.Episodes))
	})
}
