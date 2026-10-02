package deepv2

import "fmt"

// span is a task's boundary: its first and last line in the frozen source.
type span struct{ start, end int }

// indexedReport is what a proposal source is checked against: the task boundaries of one
// validated report, the length of its frozen source and that source's SHA-256.
type indexedReport struct {
	tasks    map[string]span
	outcomes map[string]string
	records  int
	sha      string
}

// reportIndex maps a session to its validated report, as report_index of v2_contract.py: the
// Deep validator holds its own session, the registry every published one.
type reportIndex map[string]indexedReport

// indexReport indexes the tasks of a report ValidateDeep has accepted, so they are well formed.
func indexReport(tasks []any, sourceSHA256 string, sourceRecords int) indexedReport {
	bounds := make(map[string]span, len(tasks))
	outcomes := make(map[string]string, len(tasks))
	for _, item := range tasks {
		task := item.(map[string]any)  //nolint:forcetypeassert // validated by validateTask
		id := task["task_id"].(string) //nolint:forcetypeassert // validated by validateTask
		start, _ := intValue(task["start_line"])
		end, _ := intValue(task["end_line"])
		bounds[id] = span{start, end}
		outcomes[id], _ = task["outcome"].(string)
	}
	return indexedReport{tasks: bounds, outcomes: outcomes, records: sourceRecords, sha: sourceSHA256}
}

// validateProposal is _validate_proposal: the keys, the fields and axes, then the sources. A
// candidate of a Deep report passes its session as candidateSession and may cite no other; a
// registry proposal passes "", may cite any indexed session and may hold recurrence.
func validateProposal(value any, path string, index reportIndex, candidateSession string) (map[string]any, error) {
	required, optional := proposalFields()
	registry := candidateSession == ""
	if registry {
		optional = append(optional, "recurrence")
	}
	p, err := object(value, path, required, optional)
	if err != nil {
		return nil, err
	}
	if err := validateProposalFields(p, path); err != nil {
		return nil, err
	}
	if err := validateProposalSources(p["sources"], path+".sources", index, candidateSession); err != nil {
		return nil, err
	}
	if v, ok := p["recurrence"]; ok && registry {
		if err := validateRecurrence(v, path+".recurrence"); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// validateProposalSources is the second half of _validate_proposal: each source names a task
// of a validated report at its frozen version, its lines lie inside that task, and no task is
// cited twice.
func validateProposalSources(value any, path string, index reportIndex, candidateSession string) error {
	sources, ok := value.([]any)
	if !ok || len(sources) == 0 {
		return fail(path, "expected nonempty array")
	}
	seen := make(map[[2]string]bool, len(sources))
	for i, item := range sources {
		sp := fmt.Sprintf("%s[%d]", path, i)
		source, err := object(item, sp, []string{"session_id", "task_id", "source_sha256", "evidence"}, nil)
		if err != nil {
			return err
		}
		sessionID, err := str(source["session_id"], sp+".session_id", true)
		if err != nil {
			return err
		}
		taskID, err := str(source["task_id"], sp+".task_id", true)
		if err != nil {
			return err
		}
		if candidateSession != "" && sessionID != candidateSession {
			return fail(sp, "per-session candidate cannot cite another session")
		}
		report, ok := index[sessionID]
		if !ok {
			return fail(sp, "source session has no validated v2 report")
		}
		bounds, ok := report.tasks[taskID]
		if !ok || source["source_sha256"] != report.sha {
			return fail(sp, "source task or frozen version mismatch")
		}
		lines, err := evidence(source["evidence"], sp+".evidence", report.records, true)
		if err != nil {
			return err
		}
		if !within(lines, bounds.start, bounds.end) {
			return fail(sp+".evidence", "proposal evidence outside cited task boundary")
		}
		key := [2]string{sessionID, taskID}
		if seen[key] {
			return fail(sp, "duplicate source task")
		}
		seen[key] = true
	}
	return nil
}
