package registry

import (
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
)

// The notes and reasons Project adds, byte for byte as project_proposal.
const (
	noteQuestioned    = " New analysis questions readiness after this version was applied; review before another change."
	reasonApplied     = "Prepared when this recorded version was applied; new analysis requires review."
	reasonDiffers     = "Target or change differs from recorded lifecycle; inspect current state."
	notePriorOnly     = " Earlier decisions and applications remain in the private lifecycle journal; a new owner decision is required."
	notePriorAndMatch = " Earlier versions remain in the private lifecycle journal."
)

// The notes and texts of the coach's projection (docs/specs/deep-review, «Проекция коуча на
// реестр»).
const (
	noteAutomation = " Coach application recorded; automation basis is required before it is shown as applied."
	errApplied     = "application already recorded for this proposal version"
	errDecided     = "cannot change decision after application"
	errBlank       = "coach record needs change, rollback and check to prepare the proposal"
)

// projection is one pass of Project over the journal for one proposal.
type projection struct {
	p           Proposal
	id          string
	fingerprint string
	state       journal.ProposalState
	prior       bool
	matched     bool
	// applied is the record_id of the coach decision that gave execution = applied; accepted is
	// the last applied or test decision projected into decision only (automation not prepared).
	applied, accepted string
}

// Project is project_proposal (docs/specs/deep-review, «Project»): the decision, execution and
// effect of the merged proposal p replayed from the owner's journal records recs, in chain
// order, the coach's decisions on p included (HT-230), and recurrence folded from the checks of
// the coach decision that applied it. Only records of p's proposal_id whose fingerprint is p's
// count; the others are earlier versions and are noted in history_review. A transition that does not hold on replay is
// not applied and is noted, so Project never fails. p is not changed.
func Project(p Proposal, recs []journal.Record) Proposal {
	result := clone(p).(Proposal) //nolint:forcetypeassert // clone keeps the type
	fp, err := journal.ProposalFingerprint(text(result, "group_key"), result["target"], result["change"])
	if err != nil {
		fp = "" // no record carries an empty fingerprint: every one is an earlier version
	}
	target, _ := result["target"].(map[string]any)
	pr := &projection{
		p: result, id: text(result, "proposal_id"), fingerprint: fp,
		state: journal.ProposalState{
			Readiness:     axisStatus(result, "readiness"),
			TargetVersion: text(target, "version"),
			Decision:      journal.DefaultDecision(),
			Execution:     journal.DefaultExecution(),
			Effect:        journal.DefaultEffect(),
		},
	}
	delete(result, "recurrence")
	for _, r := range recs {
		switch {
		case r.IsLifecycle() && r.ProposalID == pr.id:
			pr.lifecycle(r)
		case r.Kind == journal.KindCoachDecision:
			pr.coach(r)
		}
	}
	switch {
	case pr.prior && !pr.matched:
		if axisStatus(result, "readiness") != "prepared_verified" {
			result["readiness"] = map[string]any{"status": "needs_specification", "reason": reasonDiffers}
		}
		pr.note(notePriorOnly)
	case pr.prior:
		pr.note(notePriorAndMatch)
	}
	pr.writeAxes()
	pr.recurrence(recs)
	return result
}

// lifecycle replays a decision, application or effect event of the proposal.
func (pr *projection) lifecycle(r journal.Record) {
	if r.ProposalFingerprint != pr.fingerprint {
		pr.prior = true
		return
	}
	pr.matched = true
	next := pr.state
	raise := r.Kind == journal.KindApplication && next.Readiness != "prepared_verified"
	if raise {
		next.Readiness = "prepared_verified"
	}
	if err := journal.ApplyEvent(&next, r); err != nil {
		pr.notApplied(r, err)
		return
	}
	if raise {
		pr.note(noteQuestioned)
		pr.p["readiness"] = map[string]any{"status": "prepared_verified", "reason": reasonApplied}
	}
	pr.state = next
}

// notApplied notes a record whose transition does not hold on replay.
func (pr *projection) notApplied(r journal.Record, err error) {
	pr.note(" Journal record " + r.RecordID + " was not applied: " + err.Error() + ".")
}

// note appends to history_review and strips, as Project does.
func (pr *projection) note(s string) {
	pr.p["history_review"] = strings.TrimSpace(text(pr.p, "history_review") + s)
}

// writeAxes writes the replayed axes into the proposal.
func (pr *projection) writeAxes() {
	s := pr.state
	evidence := make([]any, 0, len(s.Execution.Evidence))
	for _, e := range s.Execution.Evidence {
		evidence = append(evidence, e)
	}
	pr.p["decision"] = map[string]any{"status": s.Decision.Status, "at": s.Decision.At}
	pr.p["execution"] = map[string]any{
		"status": s.Execution.Status, "at": s.Execution.At, "version": s.Execution.Version, "evidence": evidence,
	}
	pr.p["effect"] = map[string]any{
		"status": s.Effect.Status, "method": s.Effect.Method, "metric": s.Effect.Metric,
		"n_before": s.Effect.NBefore, "n_after": s.Effect.NAfter, "note": s.Effect.Note,
	}
}

// coach replays a coach decision whose findings name the proposal (docs/specs/deep-review,
// «Проекция коуча на реестр»): its fingerprint of the proposal is compared as an event's.
func (pr *projection) coach(r journal.Record) {
	e, err := journal.CoachEntryFromRecord(r)
	if err != nil || !slices.Contains(e.Findings, pr.id) {
		return
	}
	switch e.Decision {
	case "declined", "not_justified", "applied", "test":
	default:
		return
	}
	if e.ProposalFingerprints[pr.id] != pr.fingerprint {
		pr.prior = true
		return
	}
	pr.matched = true
	if pr.state.Execution.Status == "applied" {
		msg := errApplied
		if e.Decision == "declined" || e.Decision == "not_justified" {
			msg = errDecided
		}
		pr.note(" Journal record " + e.ID + " was not applied: " + msg + ".")
		return
	}
	if e.Decision == "declined" || e.Decision == "not_justified" {
		pr.state.Decision = journal.Decision{Status: "rejected", At: e.At}
		pr.note(" Coach conversation " + e.ID + " recorded " + e.Decision + "; the reason is in the decision journal.")
		return
	}
	prepare := pr.state.Readiness != "prepared_verified" && text(pr.p, "change_type") != "automation"
	if prepare && (strings.TrimSpace(e.Change) == "" || strings.TrimSpace(e.Rollback) == "" || strings.TrimSpace(e.Check) == "") {
		// ValidateCoach refuses only empty texts; a blank one cannot prepare the proposal,
		// and a raised readiness without it would not pass ValidateRegistry.
		pr.note(" Journal record " + e.ID + " was not applied: " + errBlank + ".")
		return
	}
	pr.state.Decision = journal.Decision{Status: "accepted", At: e.At}
	if pr.state.Readiness != "prepared_verified" {
		if !prepare {
			pr.accepted = e.ID
			pr.note(noteAutomation)
			return
		}
		pr.prepare(e)
	}
	version := e.AfterSHA256
	if version == "" {
		version = e.ID
	}
	pr.state.Execution = journal.Execution{Status: "applied", At: e.At, Version: version, Evidence: []string{e.ID}}
	pr.applied = e.ID
	pr.note(" Coach conversation " + e.ID + " recorded " + e.Decision + ".")
}

// prepare raises the proposal to prepared_verified from the coach decision e, filling every
// field its invariants need (the spec's table, «Проекция коуча на реестр»).
func (pr *projection) prepare(e journal.CoachEntry) {
	p := pr.p
	old, _ := p["target"].(map[string]any)
	locator := e.Target
	if strings.TrimSpace(locator) == "" {
		locator = strings.TrimSpace(text(old, "locator"))
		if locator == "" {
			locator = "coach:" + e.ID
		} else {
			locator = text(old, "locator")
		}
	}
	version := e.BeforeSHA256
	if version == "" {
		version = e.ID
	}
	layer := ""
	if e.Layer != nil {
		layer = *e.Layer
	}
	p["target"] = map[string]any{"kind": layer, "locator": locator, "version": version}
	p["change"], p["rollback"], p["verification"] = e.Change, e.Rollback, e.Check
	if blank(p, "cause") {
		p["cause"] = "Причина не уточнена в отчёте; правка подготовлена в разговоре коуча " + e.ID + "."
	}
	if blank(p, "expected_effect") {
		p["expected_effect"] = "Эффект проверяется проверкой коуча не раньше " + e.CheckAfter + "."
	}
	if pre, _ := p["preconditions"].([]any); len(pre) == 0 {
		p["preconditions"] = []any{"Правка согласована человеком в разговоре коуча " + e.ID + "."}
	}
	if axisStatus(p, "existing_rule") == "not_checked" || axisStatus(p, "existing_rule") == "" {
		if e.BeforeSHA256 != "" {
			p["existing_rule"] = map[string]any{
				"status": "found", "description": "Состояние цели до правки коуча " + e.ID + ".",
				"locator": locator, "version": e.BeforeSHA256,
			}
		} else {
			p["existing_rule"] = map[string]any{
				"status": "not_found", "description": "Прежнего состояния цели нет в записи коуча " + e.ID + ".",
				"locator": locator, "version": "",
			}
		}
	}
	p["readiness"] = map[string]any{
		"status": "prepared_verified", "reason": "Prepared in coach conversation " + e.ID + "; new analysis requires review.",
	}
	pr.state.Readiness = "prepared_verified"
	pr.state.TargetVersion = version
}

// recurrence sets the fold of the checks of the coach decision that applied the proposal, or
// else of the last one projected into decision only; none without such a decision or checks.
func (pr *projection) recurrence(recs []journal.Record) {
	id := pr.applied
	if id == "" {
		id = pr.accepted
	}
	if id == "" {
		return
	}
	rec, ok := journal.FoldChecks(recs)[id]
	if !ok {
		return
	}
	pr.p["recurrence"] = map[string]any{
		"result": rec.Result, "observations": intOrNil(rec.Observations), "repeats": intOrNil(rec.Repeats),
		"checked_at": rec.CheckedAt, "checks": rec.Checks,
	}
}

func intOrNil(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

// blank tells whether the text under key is empty after strip.
func blank(p Proposal, key string) bool {
	return strings.TrimSpace(text(p, key)) == ""
}
