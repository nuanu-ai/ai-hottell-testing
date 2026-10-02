package deepv2

// The catalogue and status sets of v2_contract.py. Each is a function returning a fresh
// slice, so no importer can change the contract for the rest of the process.

// CheckIDs are the 13 Deep checks in catalogue order; a report lists all of them in this order.
func CheckIDs() []string {
	return []string{"D01", "D03", "D05", "D07", "D10", "D12", "D13", "D16", "D19", "D22", "D24", "D25", "D26"}
}

// ReviewChecks are the independent semantic checks an approving review names exactly once.
func ReviewChecks() []string {
	return []string{"task_boundaries", "completion_claims", "all_13_checks", "proposal_choice", "sensitive_data"}
}

// CheckStatuses are the states of one of the 13 checks.
func CheckStatuses() []string {
	return []string{"suspected", "checked_clear", "insufficient_data", "not_applicable", "not_checked"}
}

// Outcomes are the states of a task's result.
func Outcomes() []string { return []string{"verified", "partial", "failed", "unknown"} }

// ChangeTypes are the kinds of change a proposal makes.
func ChangeTypes() []string {
	return []string{"personalization", "project_rule", "skill", "hook", "script", "workflow", "automation", "settings", "product", "diagnosis"}
}

// Priorities are the priorities of a proposal, lowest first.
func Priorities() []string { return []string{"low", "medium", "high"} }

// RuleStatuses are the states of the review of a rule that may already cover a proposal.
func RuleStatuses() []string { return []string{"found", "not_found", "not_checked"} }

// Readiness are the states of a proposal's preparation.
func Readiness() []string { return []string{"hypothesis", "needs_specification", "prepared_verified"} }

// Decisions are the states of the owner's decision on a proposal.
func Decisions() []string {
	return []string{"not_requested", "accepted", "rejected", "revision_requested"}
}

// Executions are the states of a proposal's application.
func Executions() []string { return []string{"not_applied", "applied"} }

// Effects are the states of a proposal's measured effect.
func Effects() []string {
	return []string{"not_measured", "helped", "no_effect", "worse", "insufficient_data"}
}

// RecurrenceResults are the results of the coach's checks of an applied decision, folded into
// a registry proposal's recurrence.
func RecurrenceResults() []string { return []string{"repeated", "not_repeated", "not_enough_data"} }

// ClaimVerifications are the states of a completion claim at the moment it was made.
func ClaimVerifications() []string {
	return []string{"verified", "unverified", "contradicted", "unknown"}
}

// ObservationStatuses are the states of an observation outside the 13 checks.
func ObservationStatuses() []string { return []string{"suspected", "confirmed", "dismissed"} }
