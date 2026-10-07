package registry

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
)

// Proposal is one proposal of the registry: a JSON object as deepv2.Decode reads it.
type Proposal = map[string]any

// Registry is the registry document of one user, {kind, schema_version, proposals}, as
// deepv2.ValidateRegistry takes it.
type Registry = map[string]any

// PublishedReport is a published Deep report of the registry's owner: the session, the
// document read by deepv2.Decode and validated, and the source_records of its source.
type PublishedReport struct {
	SessionID string
	Doc       any
	Records   int
}

// The notes Merge adds to history_review, byte for byte as merge_candidates.
const (
	noteUncertainCause = " Cause remains uncertain across source sessions; check it before preparing a change."
	noteRuleDiffers    = " Historical rule availability differs by session; verify current rules and scope."
	noteConflict       = " Conflicting details from another session require semantic review."
)

//nolint:gochecknoglobals // read-only tables, PRIORITY and READY of v2_registry.py
var (
	priorityRank  = map[string]int{"low": 0, "medium": 1, "high": 2}
	readinessRank = map[string]int{"hypothesis": 0, "needs_specification": 1, "prepared_verified": 2}
)

// Merge is merge_candidates without previous (docs/specs/deep-review, «Merge»): the
// candidates of the owner's published reports grouped by group_key into proposals whose
// proposal_id is the group key, conflicts between sessions shown instead of reconciled, each
// proposal projected onto the owner's journal records recs, ordered by priority descending,
// then group key. generated_at is not set. The reports are not changed.
func Merge(reports []PublishedReport, recs []journal.Record) Registry {
	sorted := slices.Clone(reports)
	slices.SortStableFunc(sorted, func(a, b PublishedReport) int { return cmp.Compare(a.SessionID, b.SessionID) })
	groups := map[string]Proposal{}
	var order []string
	for _, r := range sorted {
		doc, _ := r.Doc.(map[string]any)
		candidates, _ := doc["proposal_candidates"].([]any)
		for _, item := range candidates {
			c, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key := text(c, "group_key")
			current, ok := groups[key]
			if !ok {
				p := clone(c).(Proposal) //nolint:forcetypeassert // clone keeps the type
				p["proposal_id"] = key
				groups[key] = p
				order = append(order, key)
				continue
			}
			mergeInto(current, c)
		}
	}
	out := make([]Proposal, 0, len(order))
	for _, key := range order {
		p := Project(groups[key], recs)
		if n := sessionCount(p); n > 1 {
			p["observed"] = fmt.Sprintf("Похожие основания есть в %d сессиях; ссылки приведены в карточке. Пример из первой: %s",
				n, text(p, "observed"))
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Proposal) int {
		if c := cmp.Compare(priorityRank[text(b, "priority")], priorityRank[text(a, "priority")]); c != 0 {
			return c
		}
		return cmp.Compare(text(a, "group_key"), text(b, "group_key"))
	})
	list := make([]any, 0, len(out))
	for _, p := range out {
		list = append(list, p)
	}
	return Registry{"kind": "proposal_registry", "schema_version": 2, "proposals": list}
}

// mergeInto merges candidate c into current, the proposal of the same group key.
func mergeInto(current, c Proposal) {
	var conflicts []string
	for _, field := range []string{"change_type", "scope", "change_intent", "change", "verification", "rollback", "expected_effect"} {
		if !reflect.DeepEqual(current[field], c[field]) {
			conflicts = append(conflicts, field)
		}
	}
	uncertain := text(current, "change_type") == "diagnosis" && text(c, "change_type") == "diagnosis" &&
		axisStatus(current, "readiness") != "prepared_verified" && axisStatus(c, "readiness") != "prepared_verified"
	if !reflect.DeepEqual(current["cause"], c["cause"]) {
		if uncertain {
			noteOnce(current, noteUncertainCause)
		} else {
			conflicts = append(conflicts, "cause")
		}
	}
	if !reflect.DeepEqual(current["target"], c["target"]) {
		conflicts = append(conflicts, "target")
	}
	if !reflect.DeepEqual(current["existing_rule"], c["existing_rule"]) {
		mine, theirs := axisStatus(current, "existing_rule"), axisStatus(c, "existing_rule")
		if uncertain && (mine == "not_checked" || theirs == "not_checked") {
			noteOnce(current, noteRuleDiffers)
			if theirs == "not_checked" {
				current["existing_rule"] = clone(c["existing_rule"])
			}
		} else {
			conflicts = append(conflicts, "existing_rule")
		}
	}
	switch {
	case len(conflicts) > 0:
		current["readiness"] = map[string]any{
			"status": "needs_specification",
			"reason": "Conflicting session proposals: " + strings.Join(conflicts, ", ") + "; review before preparation.",
		}
		current["history_review"] = text(current, "history_review") + noteConflict
	case readinessRank[axisStatus(c, "readiness")] < readinessRank[axisStatus(current, "readiness")]:
		current["readiness"] = clone(c["readiness"])
	}
	if priorityRank[text(c, "priority")] > priorityRank[text(current, "priority")] {
		current["priority"] = c["priority"]
	}
	sources, _ := current["sources"].([]any)
	seen := map[[2]string]bool{}
	for _, s := range sources {
		seen[sourceKey(s)] = true
	}
	more, _ := c["sources"].([]any)
	for _, s := range more {
		if k := sourceKey(s); !seen[k] {
			sources = append(sources, clone(s))
			seen[k] = true
		}
	}
	current["sources"] = sources
	versions, _ := current["analysis_versions"].([]any)
	newer, _ := c["analysis_versions"].([]any)
	for _, v := range newer {
		if !slices.Contains(versions, v) {
			versions = append(versions, v)
		}
	}
	current["analysis_versions"] = versions
}

// noteOnce appends note to history_review unless it is already there, without strip, as
// Merge does.
func noteOnce(p Proposal, note string) {
	if h := text(p, "history_review"); !strings.Contains(h, note) {
		p["history_review"] = h + note
	}
}

func sourceKey(s any) [2]string {
	m, _ := s.(map[string]any)
	return [2]string{text(m, "session_id"), text(m, "task_id")}
}

func sessionCount(p Proposal) int {
	sources, _ := p["sources"].([]any)
	sessions := map[string]bool{}
	for _, s := range sources {
		m, _ := s.(map[string]any)
		sessions[text(m, "session_id")] = true
	}
	return len(sessions)
}

func text(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// axisStatus is the status of the object under key: readiness, decision, execution, effect
// or existing_rule.
func axisStatus(p Proposal, key string) string {
	m, _ := p[key].(map[string]any)
	return text(m, "status")
}

// clone deep-copies a decoded JSON value.
func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clone(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clone(e)
		}
		return out
	default:
		return v
	}
}
