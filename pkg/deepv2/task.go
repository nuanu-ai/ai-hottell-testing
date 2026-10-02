package deepv2

import "fmt"

// validateTask is _validate_task: a task is a boundary in source lines, a goal with its
// basis, completion claims inside the boundary and an optional continuation of a task of
// another session. It returns the task id.
func validateTask(value any, path string, sourceRecords int) (string, error) {
	task, err := object(value, path,
		[]string{"task_id", "goal", "goal_evidence", "scope", "start_line", "end_line", "boundary_evidence", "success_criteria", "outcome", "outcome_basis", "claims"},
		[]string{"continued_from"})
	if err != nil {
		return "", err
	}
	taskID, err := str(task["task_id"], path+".task_id", true)
	if err != nil {
		return "", err
	}
	if _, err := str(task["goal"], path+".goal", true); err != nil {
		return "", err
	}
	if _, err := str(task["scope"], path+".scope", true); err != nil {
		return "", err
	}
	start, err := positiveLine(task["start_line"], path+".start_line", sourceRecords)
	if err != nil {
		return "", err
	}
	end, err := positiveLine(task["end_line"], path+".end_line", sourceRecords)
	if err != nil {
		return "", err
	}
	if start > end {
		return "", fail(path, "task starts after it ends")
	}
	for _, key := range []string{"goal_evidence", "boundary_evidence"} {
		if _, err := evidence(task[key], path+"."+key, sourceRecords, true); err != nil {
			return "", err
		}
	}
	if _, err := strs(task["success_criteria"], path+".success_criteria", false); err != nil {
		return "", err
	}
	if _, err := choice(task["outcome"], path+".outcome", Outcomes()); err != nil {
		return "", err
	}
	if _, err := evidence(task["outcome_basis"], path+".outcome_basis", sourceRecords, true); err != nil {
		return "", err
	}
	if err := validateClaims(task["claims"], path+".claims", sourceRecords, start, end); err != nil {
		return "", err
	}
	if c, ok := task["continued_from"]; ok {
		if err := validateContinuation(c, path+".continued_from", sourceRecords, start, end); err != nil {
			return "", err
		}
	}
	return taskID, nil
}

func validateClaims(value any, path string, sourceRecords, start, end int) error {
	claims, ok := value.([]any)
	if !ok {
		return fail(path, "expected array")
	}
	for i, item := range claims {
		cp := fmt.Sprintf("%s[%d]", path, i)
		claim, err := object(item, cp, []string{"line", "claim", "verification_at_claim", "evidence", "subsequent_resolution"}, nil)
		if err != nil {
			return err
		}
		line, err := positiveLine(claim["line"], cp+".line", sourceRecords)
		if err != nil {
			return err
		}
		if line < start || line > end {
			return fail(cp+".line", "completion claim outside task boundary")
		}
		if _, err := str(claim["claim"], cp+".claim", true); err != nil {
			return err
		}
		if _, err := choice(claim["verification_at_claim"], cp+".verification_at_claim", ClaimVerifications()); err != nil {
			return err
		}
		if _, err := evidence(claim["evidence"], cp+".evidence", sourceRecords, true); err != nil {
			return err
		}
		if _, err := str(claim["subsequent_resolution"], cp+".subsequent_resolution", false); err != nil {
			return err
		}
	}
	return nil
}

// validateContinuation checks continued_from against the current task only; the previous
// session's task and lines are checked by the registry, which holds both reports.
func validateContinuation(value any, cp string, sourceRecords, start, end int) error {
	c, err := object(value, cp, []string{"session_id", "task_id", "source_sha256", "current_evidence", "previous_evidence"}, nil)
	if err != nil {
		return err
	}
	if _, err := str(c["session_id"], cp+".session_id", true); err != nil {
		return err
	}
	if _, err := str(c["task_id"], cp+".task_id", true); err != nil {
		return err
	}
	sum, err := str(c["source_sha256"], cp+".source_sha256", false)
	if err != nil {
		return err
	}
	if !sha256Re.MatchString(sum) {
		return fail(cp+".source_sha256", "invalid frozen source version")
	}
	current, err := evidence(c["current_evidence"], cp+".current_evidence", sourceRecords, true)
	if err != nil {
		return err
	}
	if !within(current, start, end) {
		return fail(cp+".current_evidence", "outside current task boundary")
	}
	previous, err := strs(c["previous_evidence"], cp+".previous_evidence", true)
	if err != nil {
		return err
	}
	for i, ref := range previous {
		if !lineRef.MatchString(ref) {
			return fail(fmt.Sprintf("%s.previous_evidence[%d]", cp, i), "expected frozen source reference L<number>")
		}
	}
	return nil
}
