package deepv2

import (
	"fmt"
	"slices"
	"time"
)

// Report is a published Deep report of one session: the decoded document and the length of
// its frozen source.
//
// CheckRefs asks for the references this report makes to other sessions to be checked. They
// are checked when the report itself is published: a later publication of a cited session on
// another source version makes such a reference stale, which is shown on reading, not an error
// that would roll back every later rebuild of the registry (deep-review spec, «Реестр как
// проекция»).
type Report struct {
	Doc       any
	Records   int
	CheckRefs bool
}

// ValidateRegistry is validate_registry: it validates each published report with
// ValidateDeep, checks the references a report with CheckRefs makes to other sessions
// (continued tasks and peer sources), then each proposal of the registry against those reports.
// Proposal id and group key are unique in the registry. reports maps a session id to its
// published report; sessions are visited in id order, so the first error is the same on every
// run.
func ValidateRegistry(registry any, reports map[string]Report) error {
	r, err := object(registry, "registry", []string{"kind", "schema_version", "proposals"}, []string{"generated_at"})
	if err != nil {
		return err
	}
	if version, isInt := intValue(r["schema_version"]); r["kind"] != "proposal_registry" || !isInt || version != 2 {
		return fail("registry", "invalid identity")
	}
	if v, ok := r["generated_at"]; ok {
		if _, err := str(v, "registry.generated_at", true); err != nil {
			return err
		}
	}
	proposals, ok := r["proposals"].([]any)
	if !ok {
		return fail("registry.proposals", "expected array")
	}
	sessions := make([]string, 0, len(reports))
	for sid := range reports {
		sessions = append(sessions, sid)
	}
	slices.Sort(sessions)
	index := make(reportIndex, len(reports))
	docs := make(map[string]map[string]any, len(reports))
	for _, sid := range sessions {
		report := reports[sid]
		doc, _ := report.Doc.(map[string]any)
		sum, _ := doc["source_sha256"].(string)
		if err := ValidateDeep(report.Doc, sid, sum, report.Records); err != nil {
			return err
		}
		docs[sid] = doc
		index[sid] = indexReport(doc["tasks"].([]any), sum, report.Records) //nolint:forcetypeassert // validated by ValidateDeep
	}
	for _, sid := range sessions {
		if !reports[sid].CheckRefs {
			continue
		}
		if err := validateCrossReferences(sid, docs[sid], index); err != nil {
			return err
		}
	}
	ids := make(map[string]bool, len(proposals))
	keys := make(map[string]bool, len(proposals))
	for i, item := range proposals {
		p, err := validateProposal(item, fmt.Sprintf("registry.proposals[%d]", i), index, "")
		if err != nil {
			return err
		}
		id, key := p["proposal_id"].(string), p["group_key"].(string) //nolint:forcetypeassert // validated by validateProposal
		if ids[id] || keys[key] {
			return fail("registry.proposals", "duplicate proposal id or group key")
		}
		ids[id], keys[key] = true, true
	}
	return nil
}

// validateCrossReferences checks the references the report of session sid makes to other
// published reports: the tasks it continues and the peer sources of its checks.
func validateCrossReferences(sid string, doc map[string]any, index reportIndex) error {
	for _, item := range doc["tasks"].([]any) { //nolint:forcetypeassert // validated by ValidateDeep
		task := item.(map[string]any) //nolint:forcetypeassert // validated by ValidateDeep
		continuation, ok := task["continued_from"].(map[string]any)
		if !ok {
			continue
		}
		path := fmt.Sprintf("%s.%s.continued_from", sid, task["task_id"])
		peer := continuation["session_id"].(string) //nolint:forcetypeassert // validated by validateTask
		previous, ok := index[peer]
		if !ok || peer == sid {
			return fail(path, "previous session missing or same as current session")
		}
		bounds, ok := previous.tasks[continuation["task_id"].(string)] //nolint:forcetypeassert // validated by validateTask
		if !ok || continuation["source_sha256"] != previous.sha {
			return fail(path, "previous task or frozen version mismatch")
		}
		lines, err := evidence(continuation["previous_evidence"], path+".previous_evidence", previous.records, true)
		if err != nil {
			return err
		}
		if !within(lines, bounds.start, bounds.end) {
			return fail(path, "previous evidence outside cited task boundary")
		}
	}
	for _, item := range doc["checks"].([]any) { //nolint:forcetypeassert // validated by ValidateDeep
		check := item.(map[string]any) //nolint:forcetypeassert // validated by ValidateDeep
		peers, _ := check["peer_sources"].([]any)
		path := fmt.Sprintf("%s.%s.peer_sources", sid, check["id"])
		seen := make(map[[2]string]bool, len(peers))
		for _, p := range peers {
			source := p.(map[string]any)          //nolint:forcetypeassert // validated by validatePeerSources
			peer := source["session_id"].(string) //nolint:forcetypeassert // validated by validatePeerSources
			taskID := source["task_id"].(string)  //nolint:forcetypeassert // validated by validatePeerSources
			report, ok := index[peer]
			if !ok || peer == sid {
				return fail(path, "peer session missing or same as local session")
			}
			bounds, ok := report.tasks[taskID]
			if !ok || source["source_sha256"] != report.sha {
				return fail(path, "peer task or frozen version mismatch")
			}
			key := [2]string{peer, taskID}
			if seen[key] {
				return fail(path, "duplicate peer task")
			}
			seen[key] = true
			if check["id"] == "D22" && report.outcomes[taskID] != "verified" {
				return fail(path, "D22 peer task must have verified outcome")
			}
			lines, err := evidence(source["evidence"], path+".evidence", report.records, true)
			if err != nil {
				return err
			}
			if !within(lines, bounds.start, bounds.end) {
				return fail(path, "peer evidence outside cited task boundary")
			}
		}
	}
	return nil
}

// validateRecurrence checks the fold of the coach's checks a registry proposal may carry, by the
// deep-review spec's table: observations is a nonnegative integer, or null for not_enough_data;
// repeats, only for repeated, lie in 1..observations.
func validateRecurrence(value any, path string) error {
	m, err := object(value, path, []string{"result", "observations", "repeats", "checked_at", "checks"}, nil)
	if err != nil {
		return err
	}
	result, err := choice(m["result"], path+".result", RecurrenceResults())
	if err != nil {
		return err
	}
	observations, isInt := intValue(m["observations"])
	if nullAllowed := result == "not_enough_data" && m["observations"] == nil; !nullAllowed && (!isInt || observations < 0) {
		return fail(path+".observations", "expected nonnegative integer, or null for not_enough_data")
	}
	repeats, isInt := intValue(m["repeats"])
	switch {
	case result == "repeated" && (!isInt || repeats < 1 || repeats > observations):
		return fail(path+".repeats", "repeated needs an integer from 1 to observations")
	case result != "repeated" && m["repeats"] != nil:
		return fail(path+".repeats", "repeats is only for repeated")
	}
	at, ok := m["checked_at"].(string)
	if _, err := time.Parse(time.RFC3339, at); !ok || err != nil {
		return fail(path+".checked_at", "expected RFC 3339 time")
	}
	if checks, isInt := intValue(m["checks"]); !isInt || checks < 1 {
		return fail(path+".checks", "expected positive integer")
	}
	return nil
}
