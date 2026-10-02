package deepv2

import "slices"

// ValidateSemanticReview is validate_semantic_review: it binds an explicit approval to one
// exact candidate version of the session. The review must name that session and candidate
// SHA-256, approve, say who reviewed it, when and why, and list each of the ReviewChecks
// exactly once. A structural pass of ValidateDeep alone does not publish a report.
func ValidateSemanticReview(review any, sessionID, candidateSHA256 string) error {
	r, err := object(review, "review", []string{"session_id", "candidate_sha256", "verdict", "reviewer", "reviewed_at", "note", "checks"}, nil)
	if err != nil {
		return err
	}
	if r["session_id"] != sessionID || r["candidate_sha256"] != candidateSHA256 || !sha256Re.MatchString(candidateSHA256) {
		return fail("review", "session or candidate version mismatch")
	}
	if r["verdict"] != "approved" {
		return fail("review.verdict", "explicit approval required")
	}
	for _, key := range []string{"reviewer", "reviewed_at", "note"} {
		if _, err := str(r[key], "review."+key, true); err != nil {
			return err
		}
	}
	checks, err := strs(r["checks"], "review.checks", true)
	if err != nil {
		return err
	}
	slices.Sort(checks)
	want := ReviewChecks()
	slices.Sort(want)
	if !slices.Equal(checks, want) {
		return fail("review.checks", "all independent semantic checks required exactly once")
	}
	return nil
}
