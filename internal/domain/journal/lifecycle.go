package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxTextRunes is how long a text of a lifecycle event may be.
const maxTextRunes = 1000

//nolint:gochecknoglobals // compiled once, never written
var (
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	lineRefRe = regexp.MustCompile(`^L[1-9][0-9]*$`)
)

// LifecycleKinds are the kinds of the lifecycle events of a proposal (EVENTS).
func LifecycleKinds() []string { return []string{KindDecision, KindApplication, KindEffect} }

// DecisionStatuses are the statuses a decision event records (DECISIONS).
func DecisionStatuses() []string { return []string{"accepted", "rejected", "revision_requested"} }

// EffectStatuses are the statuses an effect event records (EFFECTS).
func EffectStatuses() []string { return []string{"helped", "no_effect", "worse", "insufficient_data"} }

// Decision is the decision axis of a proposal.
type Decision struct {
	Status string `json:"status"`
	At     string `json:"at"`
}

// Execution is the execution axis of a proposal.
type Execution struct {
	Status   string   `json:"status"`
	At       string   `json:"at"`
	Version  string   `json:"version"`
	Evidence []string `json:"evidence"`
}

// Effect is the effect axis of a proposal.
type Effect struct {
	Status  string `json:"status"`
	Method  string `json:"method"`
	Metric  string `json:"metric"`
	NBefore int    `json:"n_before"`
	NAfter  int    `json:"n_after"`
	Note    string `json:"note"`
}

// DefaultDecision is DEFAULT_DECISION: no decision requested.
func DefaultDecision() Decision { return Decision{Status: "not_requested"} }

// DefaultExecution is DEFAULT_EXECUTION: not applied.
func DefaultExecution() Execution { return Execution{Status: "not_applied", Evidence: []string{}} }

// DefaultEffect is DEFAULT_EFFECT: not measured.
func DefaultEffect() Effect { return Effect{Status: "not_measured"} }

// ProposalState is what _apply_state reads and changes of a proposal: its readiness, the
// version of its target and its three lifecycle axes.
type ProposalState struct {
	Readiness     string
	TargetVersion string
	Decision      Decision
	Execution     Execution
	Effect        Effect
}

// ProposalFingerprint binds a lifecycle event to the exact target and change reviewed:
// sha256(Canonical({group_key, target, change})) in hex.
func ProposalFingerprint(groupKey string, target, change any) (string, error) {
	text, err := Canonical(map[string]any{"group_key": groupKey, "target": target, "change": change})
	if err != nil {
		return "", fmt.Errorf("proposal fingerprint: %w", err)
	}
	return SHA256Hex(text), nil
}

// ValidateRecord is _validate_record for a lifecycle event: its fields, its link onto
// previousHash, its detail, its masking and its hash. The colleague's actor is user_id here.
func ValidateRecord(r Record, previousHash string) error {
	if err := validateLifecycleFields(r); err != nil {
		return err
	}
	if r.PreviousHash != previousHash {
		return errors.New("invalid lifecycle event or broken hash chain")
	}
	if err := checkMasked(r); err != nil {
		return err
	}
	h, err := RecordHash(r)
	if err != nil {
		return err
	}
	if r.RecordHash != h {
		return errors.New("lifecycle journal hash mismatch")
	}
	return nil
}

// validateLifecycleFields checks what ValidateRecord checks of an event besides its link
// onto the chain, its masking and its hash.
func validateLifecycleFields(r Record) error {
	if r.SchemaVersion != 1 {
		return errors.New("invalid lifecycle journal record")
	}
	for _, f := range []struct{ name, value string }{
		{"record_id", r.RecordID},
		{"recorded_at", r.RecordedAt},
		{"proposal_id", r.ProposalID},
		{"user_id", r.UserID},
		{"authority_ref", r.AuthorityRef},
	} {
		if err := nonemptyText(f.value, f.name); err != nil {
			return err
		}
	}
	if !r.IsLifecycle() {
		return errors.New("invalid lifecycle event or broken hash chain")
	}
	if r.Coach != nil {
		return errors.New("invalid lifecycle journal record: a lifecycle event has no coach fields")
	}
	if !sha256Re.MatchString(r.ProposalFingerprint) {
		return errors.New("invalid proposal fingerprint")
	}
	if len(r.ProposalSources) == 0 {
		return errors.New("lifecycle event needs frozen proposal sources")
	}
	for _, s := range r.ProposalSources {
		if err := nonemptyText(s.SessionID, "proposal_sources.session_id"); err != nil {
			return err
		}
		if err := nonemptyText(s.TaskID, "proposal_sources.task_id"); err != nil {
			return err
		}
		if !sha256Re.MatchString(s.SourceSHA256) {
			return errors.New("invalid lifecycle source version")
		}
		if len(s.Evidence) == 0 {
			return errors.New("proposal_sources.evidence: expected nonempty array")
		}
		for _, line := range s.Evidence {
			if err := nonemptyText(line, "proposal_sources.evidence"); err != nil {
				return err
			}
			if !lineRefRe.MatchString(line) {
				return errors.New("invalid lifecycle source line")
			}
		}
	}
	return ValidateDetail(r.Kind, r.Detail)
}

// ValidateDetail is _validate_detail: the detail of a decision, an application or an effect.
func ValidateDetail(kind string, detail map[string]any) error {
	if !slices.Contains(LifecycleKinds(), kind) {
		return errors.New("invalid lifecycle event")
	}
	if detail == nil {
		return errors.New("detail: expected object")
	}
	switch kind {
	case KindDecision:
		status, ok := detail["status"].(string)
		if !hasKeys(detail, "status") || !ok || !slices.Contains(DecisionStatuses(), status) {
			return errors.New("decision detail needs accepted, rejected or revision_requested")
		}
	case KindApplication:
		if !hasKeys(detail, "version", "target_version_verified", "evidence", "permission_ref", "rule_review") {
			return errors.New("application detail needs version, target_version_verified, evidence, permission_ref and rule_review")
		}
		for _, k := range []string{"version", "target_version_verified", "permission_ref", "rule_review"} {
			if err := nonemptyAny(detail[k], "detail."+k); err != nil {
				return err
			}
		}
		if _, err := texts(detail["evidence"], "detail.evidence"); err != nil {
			return err
		}
	default:
		return validateEffect(detail)
	}
	return nil
}

func validateEffect(detail map[string]any) error {
	status, ok := detail["status"].(string)
	if !hasKeys(detail, "status", "method", "metric", "n_before", "n_after", "note", "evidence") ||
		!ok || !slices.Contains(EffectStatuses(), status) {
		return errors.New("effect detail has invalid fields or status")
	}
	for _, k := range []string{"method", "metric", "note"} {
		s, ok := detail[k].(string)
		if !ok || utf8.RuneCountInString(s) > maxTextRunes {
			return fmt.Errorf("detail.%s: expected text up to 1000 characters", k)
		}
	}
	for _, k := range []string{"n_before", "n_after"} {
		if n, ok := intValue(detail[k]); !ok || n < 0 {
			return fmt.Errorf("detail.%s: expected nonnegative integer", k)
		}
	}
	if _, err := texts(detail["evidence"], "detail.evidence"); err != nil {
		return err
	}
	if status == "insufficient_data" {
		return nonemptyAny(detail["note"], "detail.note")
	}
	for _, k := range []string{"method", "metric"} {
		if err := nonemptyAny(detail[k], "detail."+k); err != nil {
			return err
		}
	}
	if n, _ := intValue(detail["n_before"]); n == 0 {
		return errors.New("measured effect needs observations before and after")
	}
	if n, _ := intValue(detail["n_after"]); n == 0 {
		return errors.New("measured effect needs observations before and after")
	}
	return nil
}

// ApplyEvent is _apply_state: it checks that the event may follow the state and applies it.
// The detail is the one ValidateDetail accepted.
func ApplyEvent(state *ProposalState, r Record) error {
	switch r.Kind {
	case KindDecision:
		if state.Execution.Status == "applied" {
			return errors.New("cannot change decision after application")
		}
		status, _ := r.Detail["status"].(string)
		state.Decision = Decision{Status: status, At: r.RecordedAt}
	case KindApplication:
		if state.Readiness != "prepared_verified" || state.Decision.Status != "accepted" {
			return errors.New("application requires prepared proposal and accepted decision")
		}
		if state.Execution.Status == "applied" {
			return errors.New("application already recorded for this proposal version")
		}
		if verified, _ := r.Detail["target_version_verified"].(string); verified != state.TargetVersion {
			return errors.New("target version changed; prepare a new proposal")
		}
		version, _ := r.Detail["version"].(string)
		evidence, _ := texts(r.Detail["evidence"], "detail.evidence")
		state.Execution = Execution{Status: "applied", At: r.RecordedAt, Version: version, Evidence: evidence}
	case KindEffect:
		if state.Execution.Status != "applied" {
			return errors.New("effect cannot precede application")
		}
		e := Effect{}
		e.Status, _ = r.Detail["status"].(string)
		e.Method, _ = r.Detail["method"].(string)
		e.Metric, _ = r.Detail["metric"].(string)
		e.Note, _ = r.Detail["note"].(string)
		e.NBefore, _ = intValue(r.Detail["n_before"])
		e.NAfter, _ = intValue(r.Detail["n_after"])
		state.Effect = e
	default:
		return errors.New("invalid lifecycle event")
	}
	return nil
}

// RejectAppliedDuplicates is reject_applied_duplicates of v2_worker.py: an applied exact
// change may be observed, but cannot be proposed anew. candidates are the
// proposal_candidates of a Deep report; recs are the journal records of the report's author.
func RejectAppliedDuplicates(candidates []any, recs []Record) error {
	applied := map[[2]string]bool{}
	for _, r := range recs {
		if r.Kind == KindApplication {
			applied[[2]string{r.ProposalID, r.ProposalFingerprint}] = true
		}
	}
	for _, c := range candidates {
		p, _ := c.(map[string]any)
		groupKey, _ := p["group_key"].(string)
		fp, err := ProposalFingerprint(groupKey, p["target"], p["change"])
		if err != nil {
			return err
		}
		if applied[[2]string{groupKey, fp}] {
			return errors.New("proposal repeats a change already recorded as applied in lifecycle journal")
		}
	}
	return nil
}

// hasKeys reports whether m holds exactly the keys, as set(detail) == {...} does.
func hasKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// nonemptyText is _nonempty: a text with something besides spaces, up to 1000 characters.
func nonemptyText(s, name string) error {
	if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > maxTextRunes {
		return fmt.Errorf("%s: expected nonempty text up to 1000 characters", name)
	}
	return nil
}

func nonemptyAny(v any, name string) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%s: expected nonempty text up to 1000 characters", name)
	}
	return nonemptyText(s, name)
}

// texts is _strings(required=True): a nonempty array of nonempty texts.
func texts(v any, name string) ([]string, error) {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case []string:
		for _, s := range x {
			items = append(items, s)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s: expected nonempty array", name)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if err := nonemptyAny(item, name); err != nil {
			return nil, err
		}
		out = append(out, item.(string)) //nolint:forcetypeassert // nonemptyAny checked it
	}
	return out, nil
}

// intValue reads a JSON integer like type(value) is int: an int, an int64, a json.Number
// written as an integer or a float64 holding one exactly; not a bool, not 4.0 as written.
func intValue(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case json.Number:
		if !integerRe.MatchString(string(x)) {
			return 0, false
		}
		n, err := strconv.Atoi(string(x))
		return n, err == nil
	case float64:
		if x != math.Trunc(x) || math.Abs(x) >= maxExactFloat {
			return 0, false
		}
		return int(x), true
	default:
		return 0, false
	}
}
