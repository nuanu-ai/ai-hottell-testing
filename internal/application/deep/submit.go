package deep

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// SubmitInput is a Deep v2 candidate as deep_submit takes it: the report as a JSON text and,
// from session_source, the session, its agent, the path of its transcript and the frozen
// source version and length.
type SubmitInput struct {
	SessionID     string
	Agent         string
	Path          string
	SourceSHA256  string
	SourceRecords int
	Report        []byte
}

// Submit accepts a Deep v2 candidate of the user with userID (deep_submit; deep-review spec,
// «Кандидат → проверка → публикация», step 1, port of process_request and
// reject_applied_duplicates of v2_worker.py, without a model run: the agent sends the report).
//
// Every string of the report is masked; an empty group_key of a candidate is filled with
// deepv2.GroupKey and an empty proposal_id with that key; candidate_sha256 is the sha256 of
// the canonical text of that report, which is what is kept. A version of the user and
// session with the same candidate_sha256, candidate or published, is returned as is and
// nothing else is checked or written. Otherwise the report passes ValidateDeep, asserts no
// human decision, repeats no change the user's journal records as applied, has its source
// prefix compared for information and is kept as the session's candidate. An invalid input
// is a *deepv2.ValidationError.
func (s *Service) Submit(ctx context.Context, userID uuid.UUID, in SubmitInput) (domain.DeepReport, error) {
	if in.Agent != "codex" && in.Agent != "claude" {
		return domain.DeepReport{}, invalid("agent", "expected codex or claude")
	}
	if strings.TrimSpace(in.Path) == "" {
		return domain.DeepReport{}, invalid("path", "expected nonempty text")
	}
	doc, err := decodeMasked(in.Report, "deep")
	if err != nil {
		return domain.DeepReport{}, err
	}
	fillGroupKeys(doc)
	text, err := journal.Canonical(doc)
	if err != nil {
		return domain.DeepReport{}, invalid("deep", "integer numbers only")
	}
	sum := journal.SHA256Hex(text)

	if live, ok, err := s.liveVersion(ctx, userID, in.SessionID, sum); err != nil || ok {
		return live, err
	}
	if err := deepv2.ValidateDeep(doc, in.SessionID, in.SourceSHA256, in.SourceRecords); err != nil {
		return domain.DeepReport{}, err //nolint:wrapcheck // the validator's text with its path is the answer
	}
	candidates, _ := doc.(map[string]any)["proposal_candidates"].([]any) //nolint:forcetypeassert // ValidateDeep checked it
	if assertsDecision(candidates) {
		return domain.DeepReport{}, invalid(in.SessionID, "analysis candidate cannot assert a human decision or applied effect")
	}
	if err := s.rejectAppliedDuplicates(ctx, userID, candidates); err != nil {
		return domain.DeepReport{}, err
	}

	stored, err := s.deepReports.InsertCandidate(ctx, domain.DeepReport{
		UserID: userID, SessionID: in.SessionID, Agent: in.Agent, SourceSHA256: in.SourceSHA256,
		SourceRecords: in.SourceRecords, CandidateSHA256: sum, Document: text,
		SourceCheck: s.sourceCheck(ctx, userID, in),
	})
	if err != nil {
		return domain.DeepReport{}, fmt.Errorf("store Deep candidate: %w", err)
	}
	return stored, nil
}

// liveVersion finds the user's candidate or published version of the session with the
// candidate_sha256 sum.
func (s *Service) liveVersion(
	ctx context.Context, userID uuid.UUID, sessionID, sum string,
) (domain.DeepReport, bool, error) {
	versions, err := s.deepReports.List(ctx, domain.DeepReportFilter{UserID: userID})
	if err != nil {
		return domain.DeepReport{}, false, fmt.Errorf("read Deep reports: %w", err)
	}
	for _, v := range versions {
		if v.SessionID == sessionID && v.CandidateSHA256 == sum && v.Status != domain.DeepReportSuperseded {
			return v, true, nil
		}
	}
	return domain.DeepReport{}, false, nil
}

// fillGroupKeys fills an empty group_key of each candidate from its pattern, scope and change
// intent, and an empty proposal_id with the group key. A candidate whose fields cannot make a
// key is left for ValidateDeep to reject.
func fillGroupKeys(doc any) {
	r, _ := doc.(map[string]any)
	candidates, _ := r["proposal_candidates"].([]any)
	for _, c := range candidates {
		p, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if p["group_key"] == "" {
			pattern, _ := p["pattern_id"].(string)
			scope, _ := p["scope"].(string)
			intent, _ := p["change_intent"].(string)
			if key, err := deepv2.GroupKey(pattern, scope, intent); err == nil {
				p["group_key"] = key
			}
		}
		if p["proposal_id"] == "" {
			p["proposal_id"] = p["group_key"]
		}
	}
}

// assertsDecision reports whether a candidate states a decision, an application or an effect
// other than the defaults: only a person records those, in the journal.
func assertsDecision(candidates []any) bool {
	defaults := map[string]string{"decision": "not_requested", "execution": "not_applied", "effect": "not_measured"}
	for _, c := range candidates {
		p, _ := c.(map[string]any)
		for axis, status := range defaults {
			if a, _ := p[axis].(map[string]any); a["status"] != status {
				return true
			}
		}
	}
	return false
}

// rejectAppliedDuplicates is reject_applied_duplicates over the user's journal: a candidate
// with the group key and fingerprint of an application event cannot be proposed anew.
func (s *Service) rejectAppliedDuplicates(ctx context.Context, userID uuid.UUID, candidates []any) error {
	recs, err := s.journal.Records(ctx, userID)
	if err != nil {
		return fmt.Errorf("read decision journal: %w", err)
	}
	for i, c := range candidates {
		if err := journal.RejectAppliedDuplicates([]any{c}, recs); err != nil {
			return invalid(fmt.Sprintf("deep.proposal_candidates[%d]", i), err.Error())
		}
	}
	return nil
}

// sourceCheck compares the report's source version with the prefix of the stored lines; it
// only informs, so a store that cannot tell gives not_checked.
func (s *Service) sourceCheck(ctx context.Context, userID uuid.UUID, in SubmitInput) string {
	key := telemetry.TranscriptKey{
		UserID: userID, Agent: in.Agent, SessionID: in.SessionID,
		File: strings.TrimSuffix(path.Base(in.Path), ".zst"),
	}
	prefix, err := s.transcripts.SourcePrefix(ctx, key, in.SourceRecords)
	switch {
	case err != nil:
		return domain.SourceCheckNotChecked
	case prefix == in.SourceSHA256:
		return domain.SourceCheckMatched
	default:
		return domain.SourceCheckMismatch
	}
}
