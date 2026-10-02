package deepv2

// validateProposalFields is the first half of _validate_proposal: the fields of a proposal
// whose keys object has already checked, and the invariants that tie its four independent
// axes (readiness, decision, execution, effect) together.
func validateProposalFields(p map[string]any, path string) error {
	texts := make(map[string]string, 8)
	for _, key := range []string{"proposal_id", "group_key", "pattern_id", "change_intent", "action", "observed", "selection_reason", "scope"} {
		s, err := str(p[key], path+"."+key, true)
		if err != nil {
			return err
		}
		texts[key] = s
	}
	if key, err := GroupKey(texts["pattern_id"], texts["scope"], texts["change_intent"]); err != nil || texts["group_key"] != key {
		return fail(path+".group_key", "does not match pattern, scope and change intent")
	}
	for _, key := range []string{"cause", "change", "verification", "rollback", "expected_effect"} {
		if _, err := str(p[key], path+"."+key, false); err != nil {
			return err
		}
	}
	var preconditions []string
	for _, key := range []string{"alternative_causes", "preconditions", "exceptions", "analysis_versions"} {
		l, err := strs(p[key], path+"."+key, false)
		if err != nil {
			return err
		}
		if key == "preconditions" {
			preconditions = l
		}
	}
	changeType, err := choice(p["change_type"], path+".change_type", ChangeTypes())
	if err != nil {
		return err
	}
	if _, err := choice(p["priority"], path+".priority", Priorities()); err != nil {
		return err
	}
	_, rule, err := axis(p["existing_rule"], path+".existing_rule", []string{"status", "description", "locator", "version"},
		RuleStatuses(), []string{"description", "locator", "version"})
	if err != nil {
		return err
	}
	_, target, err := axis(p["target"], path+".target", []string{"kind", "locator", "version"}, nil, []string{"kind", "locator", "version"})
	if err != nil {
		return err
	}
	readiness, err := validateReadiness(p["readiness"], path+".readiness")
	if err != nil {
		return err
	}
	_, decision, err := axis(p["decision"], path+".decision", []string{"status", "at"}, Decisions(), []string{"at"})
	if err != nil {
		return err
	}
	execution, err := validateExecution(p["execution"], path+".execution")
	if err != nil {
		return err
	}
	effect, err := validateEffect(p["effect"], path+".effect")
	if err != nil {
		return err
	}
	prepared := readiness == "prepared_verified"
	if prepared {
		for _, key := range []string{"cause", "change", "verification", "rollback", "expected_effect"} {
			if _, err := str(p[key], path+"."+key, true); err != nil {
				return err
			}
		}
		for _, key := range []string{"kind", "locator", "version"} {
			if _, err := str(target[key], path+".target."+key, true); err != nil {
				return err
			}
		}
		if rule["status"] == "not_checked" || len(preconditions) == 0 {
			return fail(path, "prepared proposal needs existing-rule review and preconditions")
		}
	}
	if execution.status == "applied" {
		if !prepared || decision["status"] != "accepted" || execution.at == "" || execution.version == "" || execution.evidence == 0 {
			return fail(path, "applied change needs prepared change, accepted decision, time, version, evidence")
		}
	} else if effect.status != "not_measured" {
		return fail(path, "effect cannot be measured before application")
	}
	measured := effect.status == "helped" || effect.status == "no_effect" || effect.status == "worse"
	if measured && (effect.method == "" || effect.metric == "" || effect.nBefore == 0 || effect.nAfter == 0) {
		return fail(path, "measured effect needs method, metric, before and after samples")
	}
	if changeType == "automation" && prepared {
		if err := validateAutomationBasis(p["automation_basis"], path+".automation_basis"); err != nil {
			return err
		}
	}
	if v, ok := p["history_review"]; ok {
		if _, err := str(v, path+".history_review", false); err != nil {
			return err
		}
	}
	return nil
}

// axis reads an object of exactly keys: status, one of statusAllowed, when that is set, then
// the string fields texts in order; the other keys are left to the caller. The status and the
// texts come back as strings.
func axis(value any, path string, keys, statusAllowed, texts []string) (map[string]any, map[string]string, error) {
	m, err := object(value, path, keys, nil)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]string, len(texts)+1)
	if statusAllowed != nil {
		s, err := choice(m["status"], path+".status", statusAllowed)
		if err != nil {
			return nil, nil, err
		}
		out["status"] = s
	}
	for _, key := range texts {
		s, err := str(m[key], path+"."+key, false)
		if err != nil {
			return nil, nil, err
		}
		out[key] = s
	}
	return m, out, nil
}

// validateReadiness returns the readiness status; its reason is required.
func validateReadiness(value any, path string) (string, error) {
	m, fields, err := axis(value, path, []string{"status", "reason"}, Readiness(), nil)
	if err != nil {
		return "", err
	}
	if _, err := str(m["reason"], path+".reason", true); err != nil {
		return "", err
	}
	return fields["status"], nil
}

type executionAxis struct {
	status, at, version string
	evidence            int
}

func validateExecution(value any, path string) (executionAxis, error) {
	m, fields, err := axis(value, path, []string{"status", "at", "version", "evidence"}, Executions(), []string{"at", "version"})
	if err != nil {
		return executionAxis{}, err
	}
	evidence, err := strs(m["evidence"], path+".evidence", false)
	if err != nil {
		return executionAxis{}, err
	}
	return executionAxis{status: fields["status"], at: fields["at"], version: fields["version"], evidence: len(evidence)}, nil
}

type effectAxis struct {
	status, method, metric string
	nBefore, nAfter        int
}

func validateEffect(value any, path string) (effectAxis, error) {
	m, fields, err := axis(value, path, []string{"status", "method", "metric", "n_before", "n_after", "note"},
		Effects(), []string{"method", "metric", "note"})
	if err != nil {
		return effectAxis{}, err
	}
	counts := make([]int, 0, 2)
	for _, key := range []string{"n_before", "n_after"} {
		n, ok := intValue(m[key])
		if !ok || n < 0 {
			return effectAxis{}, fail(path+"."+key, "expected nonnegative integer")
		}
		counts = append(counts, n)
	}
	return effectAxis{status: fields["status"], method: fields["method"], metric: fields["metric"], nBefore: counts[0], nAfter: counts[1]}, nil
}

// validateAutomationBasis requires the basis a prepared automation rests on: its steps, how
// often it runs and what it gives, which automations were checked, how it handles exceptions,
// what it may touch and how it is stopped.
func validateAutomationBasis(value any, path string) error {
	keys := []string{"frequency", "benefit", "existing_automations_checked", "exception_handling", "permission_scope", "stop_method"}
	m, err := object(value, path, append([]string{"steps"}, keys...), nil)
	if err != nil {
		return err
	}
	if _, err := strs(m["steps"], path+".steps", true); err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := str(m[key], path+"."+key, true); err != nil {
			return err
		}
	}
	return nil
}
