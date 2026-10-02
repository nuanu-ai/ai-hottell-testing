package deepv2

import (
	"fmt"
	"slices"
)

// proposalFields are the keys of a proposal of v2_contract.py's _validate_proposal.
func proposalFields() (required, optional []string) {
	return []string{
			"proposal_id", "group_key", "pattern_id", "change_intent", "action", "observed", "cause",
			"alternative_causes", "existing_rule", "change_type", "selection_reason", "scope", "target",
			"change", "preconditions", "exceptions", "verification", "rollback", "expected_effect",
			"priority", "readiness", "decision", "execution", "effect", "sources", "analysis_versions",
		},
		[]string{"automation_basis", "history_review"}
}

// ValidateDeep is validate_deep: it rejects a malformed Deep v2 report of the session or a
// reference outside its frozen source of sourceRecords lines with SHA-256 sourceSHA256.
// report is a decoded JSON object, from Decode or built of map[string]any, []any, string and
// int values.
//
// Proposal candidates are checked as registry proposals are, except that they cite only this
// session's tasks and hold one candidate per group key.
func ValidateDeep(report any, sessionID, sourceSHA256 string, sourceRecords int) error {
	r, err := object(report, "deep",
		[]string{"kind", "schema_version", "session_id", "source_sha256", "tasks", "checks", "observations", "proposal_candidates", "unknowns"},
		[]string{"analysis_version", "previous_report"})
	if err != nil {
		return err
	}
	version, isInt := intValue(r["schema_version"])
	if r["kind"] != "deep" || !isInt || version != 2 || r["session_id"] != sessionID ||
		r["source_sha256"] != sourceSHA256 || !sha256Re.MatchString(sourceSHA256) {
		return fail("deep", "identity or source version mismatch")
	}
	if sourceRecords < 1 {
		return fail("source_records", "expected positive frozen source length")
	}
	if v, ok := r["analysis_version"]; ok {
		if _, err := str(v, "deep.analysis_version", true); err != nil {
			return err
		}
	}
	if v, ok := r["previous_report"]; ok {
		if _, err := str(v, "deep.previous_report", false); err != nil {
			return err
		}
	}
	tasks, ok := r["tasks"].([]any)
	if !ok || len(tasks) == 0 {
		return fail("deep.tasks", "at least one task required")
	}
	taskIDs, err := validateTasks(tasks, sessionID, sourceRecords)
	if err != nil {
		return err
	}
	checks, err := validateChecks(r["checks"], sourceRecords, taskIDs)
	if err != nil {
		return err
	}
	observations, ok := r["observations"].([]any)
	if !ok {
		return fail("deep.observations", "expected array")
	}
	for i, o := range observations {
		if err := validateObservation(o, fmt.Sprintf("deep.observations[%d]", i), sourceRecords, taskIDs); err != nil {
			return err
		}
	}
	if _, err := strs(r["unknowns"], "deep.unknowns", false); err != nil {
		return err
	}
	if checks[2]["status"] == "suspected" && !hasDoubtfulClaim(tasks) {
		return fail("deep.checks[2]", "D05 signal needs an unverified or contradicted completion claim")
	}
	index := reportIndex{sessionID: indexReport(tasks, sourceSHA256, sourceRecords)}
	return validateCandidates(r["proposal_candidates"], sessionID, index)
}

func validateTasks(tasks []any, sessionID string, sourceRecords int) ([]string, error) {
	ids := make([]string, 0, len(tasks))
	for i, task := range tasks {
		id, err := validateTask(task, fmt.Sprintf("deep.tasks[%d]", i), sourceRecords)
		if err != nil {
			return nil, err
		}
		if slices.Contains(ids, id) {
			return nil, fail("deep.tasks", "duplicate task id")
		}
		ids = append(ids, id)
	}
	for _, task := range tasks {
		if c, ok := task.(map[string]any)["continued_from"].(map[string]any); ok && c["session_id"] == sessionID {
			return nil, fail("deep.tasks", "a task cannot continue from its own session")
		}
	}
	return ids, nil
}

// validateChecks requires the 13 checks in catalogue order and validates each.
func validateChecks(value any, sourceRecords int, taskIDs []string) ([]map[string]any, error) {
	items, ok := value.([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		m, isObject := item.(map[string]any)
		id, isString := m["id"].(string)
		if !isObject || !isString {
			ok = false
			break
		}
		ids = append(ids, id)
	}
	if !ok || !slices.Equal(ids, CheckIDs()) {
		return nil, fail("deep.checks", "all 13 checks required in catalogue order")
	}
	checks := make([]map[string]any, 0, len(items))
	for i, item := range items {
		c, err := validateCheck(item, fmt.Sprintf("deep.checks[%d]", i), sourceRecords, taskIDs)
		if err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	return checks, nil
}

// validateCheck is _validate_check: suspected and checked_clear need lines and tasks,
// insufficient_data needs missing_data.
func validateCheck(value any, path string, sourceRecords int, taskIDs []string) (map[string]any, error) {
	check, err := object(value, path, []string{"id", "status", "summary", "evidence", "missing_data", "task_ids"}, []string{"peer_sources"})
	if err != nil {
		return nil, err
	}
	if _, err := choice(check["id"], path+".id", CheckIDs()); err != nil {
		return nil, err
	}
	status, err := choice(check["status"], path+".status", CheckStatuses())
	if err != nil {
		return nil, err
	}
	assessed := status == "suspected" || status == "checked_clear"
	if _, err := str(check["summary"], path+".summary", true); err != nil {
		return nil, err
	}
	if _, err := evidence(check["evidence"], path+".evidence", sourceRecords, assessed); err != nil {
		return nil, err
	}
	if _, err := strs(check["missing_data"], path+".missing_data", status == "insufficient_data"); err != nil {
		return nil, err
	}
	ids, err := knownTaskIDs(check["task_ids"], path+".task_ids", taskIDs)
	if err != nil {
		return nil, err
	}
	if assessed && len(ids) == 0 {
		return nil, fail(path, "assessed check must identify tasks")
	}
	if peers, ok := check["peer_sources"]; ok {
		if err := validatePeerSources(peers, path+".peer_sources"); err != nil {
			return nil, err
		}
	}
	return check, nil
}

// validatePeerSources checks the shape of a check's peer sources; whether the peer session,
// task and lines exist is checked by the registry, which holds the peer's report.
func validatePeerSources(value any, path string) error {
	sources, ok := value.([]any)
	if !ok {
		return fail(path, "expected array")
	}
	for i, item := range sources {
		sp := fmt.Sprintf("%s[%d]", path, i)
		source, err := object(item, sp, []string{"session_id", "task_id", "source_sha256", "evidence"}, nil)
		if err != nil {
			return err
		}
		for _, key := range []string{"session_id", "task_id"} {
			if _, err := str(source[key], sp+"."+key, true); err != nil {
				return err
			}
		}
		if sum, ok := source["source_sha256"].(string); !ok || !sha256Re.MatchString(sum) {
			return fail(sp+".source_sha256", "expected SHA-256")
		}
		refs, err := strs(source["evidence"], sp+".evidence", true)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if !lineRef.MatchString(ref) {
				return fail(sp+".evidence", "expected L<number> reference")
			}
		}
	}
	return nil
}

// validateObservation is _validate_observation: a finding outside the 13 checks, tied to tasks.
func validateObservation(value any, path string, sourceRecords int, taskIDs []string) error {
	o, err := object(value, path, []string{"pattern", "finding", "status", "evidence", "task_ids"}, nil)
	if err != nil {
		return err
	}
	for _, key := range []string{"pattern", "finding"} {
		if _, err := str(o[key], path+"."+key, true); err != nil {
			return err
		}
	}
	if _, err := choice(o["status"], path+".status", ObservationStatuses()); err != nil {
		return err
	}
	if _, err := evidence(o["evidence"], path+".evidence", sourceRecords, true); err != nil {
		return err
	}
	ids, err := knownTaskIDs(o["task_ids"], path+".task_ids", taskIDs)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fail(path, "observation must identify a task")
	}
	return nil
}

// knownTaskIDs is _task_ids: an array of distinct ids of the report's tasks.
func knownTaskIDs(value any, path string, taskIDs []string) ([]string, error) {
	ids, err := strs(value, path, false)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		if slices.Contains(ids[:i], id) || !slices.Contains(taskIDs, id) {
			return nil, fail(path, "duplicate or unknown task id")
		}
	}
	return ids, nil
}

// hasDoubtfulClaim reports whether a task holds a completion claim that was unverified or
// contradicted when it was made: the D05 signal rests on one.
func hasDoubtfulClaim(tasks []any) bool {
	for _, task := range tasks {
		claims, _ := task.(map[string]any)["claims"].([]any)
		for _, c := range claims {
			if v := c.(map[string]any)["verification_at_claim"]; v == "unverified" || v == "contradicted" {
				return true
			}
		}
	}
	return false
}

// validateCandidates checks each candidate against the report's own session only, and one
// candidate per group key.
func validateCandidates(value any, sessionID string, index reportIndex) error {
	candidates, ok := value.([]any)
	if !ok {
		return fail("deep.proposal_candidates", "expected array")
	}
	seen := make([]string, 0, len(candidates))
	for i, item := range candidates {
		p, err := validateProposal(item, fmt.Sprintf("deep.proposal_candidates[%d]", i), index, sessionID)
		if err != nil {
			return err
		}
		key := p["group_key"].(string) //nolint:forcetypeassert // validateProposal checked it
		if slices.Contains(seen, key) {
			return fail("deep.proposal_candidates", "duplicate group key in one session")
		}
		seen = append(seen, key)
	}
	return nil
}
