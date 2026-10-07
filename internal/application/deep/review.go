package deep

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/internal/domain/registry"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// Review publishes the candidate with candidateID of the user with userID on an independent
// semantic review (deep_review_submit; deep-review spec, «Кандидат → проверка → публикация»,
// step 3, port of publish of v2_publish_new.py). review is the review object as a JSON text.
//
// In one transaction, the journal lock taken first: the candidate is the caller's own (another
// user's is domain.ErrDeepReportNotFound, as a missing one); one already published whose review
// holds is returned as is, nothing written; otherwise it must still be a candidate
// (domain.ErrDeepReportNotCandidate). The review must approve exactly this candidate's bytes,
// the stored report passes ValidateDeep again with every check finished, and no candidate
// repeats a change the journal records as applied. Then the review is kept, the candidate
// published, the session's previous publication superseded, and the user's registry rebuilt;
// any failure rolls all of it back. An invalid input is a *deepv2.ValidationError. Every string
// of the review is masked before it is validated and kept, its canonical masked text stored.
// The second result is how many proposals of the registry this publication changed, counted
// in the same transaction; a repeat that writes nothing changed none (HT-402).
func (s *Service) Review(
	ctx context.Context, userID, candidateID uuid.UUID, review []byte,
) (domain.DeepReport, int, error) {
	reviewDoc, err := decodeMasked(review, "review")
	if err != nil {
		return domain.DeepReport{}, 0, err
	}
	masked, err := journal.Canonical(reviewDoc)
	if err != nil {
		return domain.DeepReport{}, 0, fmt.Errorf("review document: %w", err)
	}
	var (
		published domain.DeepReport
		changed   int
	)
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.journal.Lock(ctx); err != nil {
			return fmt.Errorf("lock decision journal: %w", err)
		}
		c, err := s.deepReports.Candidate(ctx, userID, candidateID)
		if err != nil {
			return fmt.Errorf("read Deep candidate: %w", err)
		}
		if err := deepv2.ValidateSemanticReview(reviewDoc, c.SessionID, c.CandidateSHA256); err != nil {
			return err //nolint:wrapcheck // the validator's text with its path is the answer
		}
		switch c.Status {
		case domain.DeepReportPublished:
			published = c
			return nil
		case domain.DeepReportCandidate:
		default:
			return domain.ErrDeepReportNotCandidate
		}
		recs, err := s.journal.Records(ctx, userID)
		if err != nil {
			return fmt.Errorf("read decision journal: %w", err)
		}
		if err := checkStored(c, recs); err != nil {
			return err
		}
		r, _ := reviewDoc.(map[string]any) //nolint:forcetypeassert // ValidateSemanticReview checked it
		reviewer, _ := r["reviewer"].(string)
		reviewedAt, _ := r["reviewed_at"].(string)
		published, err = s.deepReports.Publish(ctx, userID, c.ID, domain.DeepReview{
			Review: masked, Reviewer: reviewer, ReviewedAt: reviewedAt,
		})
		if err != nil {
			return fmt.Errorf("publish Deep candidate: %w", err)
		}
		changed, err = s.rebuild(ctx, userID, recs, c.SessionID)
		return err
	})
	if err != nil {
		return domain.DeepReport{}, 0, err //nolint:wrapcheck // the reason is the answer
	}
	return published, changed, nil
}

// RebuildRegistry rebuilds the registry of the user with userID from their published Deep
// reports and the journal (refresh_published_registry), in one transaction under the journal
// lock; a registry that does not pass ValidateRegistry is not written.
func (s *Service) RebuildRegistry(ctx context.Context, userID uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error { //nolint:wrapcheck // the reason is the answer
		if err := s.journal.Lock(ctx); err != nil {
			return fmt.Errorf("lock decision journal: %w", err)
		}
		recs, err := s.journal.Records(ctx, userID)
		if err != nil {
			return fmt.Errorf("read decision journal: %w", err)
		}
		_, err = s.rebuild(ctx, userID, recs, "")
		return err
	})
}

// checkStored checks the stored candidate again: ValidateDeep over its canonical text read by
// deepv2.Decode, no check left not_checked, no candidate repeating an applied change.
func checkStored(c domain.DeepReport, recs []journal.Record) error {
	doc, err := deepv2.Decode(c.Document)
	if err != nil {
		return fmt.Errorf("stored Deep candidate %s: %w", c.ID, err)
	}
	if err := deepv2.ValidateDeep(doc, c.SessionID, c.SourceSHA256, c.SourceRecords); err != nil {
		return err //nolint:wrapcheck // the validator's text with its path is the answer
	}
	m, _ := doc.(map[string]any) //nolint:forcetypeassert // ValidateDeep checked it
	checks, _ := m["checks"].([]any)
	var unfinished []string
	for _, item := range checks {
		check, _ := item.(map[string]any)
		if check["status"] == "not_checked" {
			id, _ := check["id"].(string)
			unfinished = append(unfinished, id)
		}
	}
	if len(unfinished) > 0 {
		return invalid("unfinished Deep checks", strings.Join(unfinished, ", "))
	}
	candidates, _ := m["proposal_candidates"].([]any)
	for i, item := range candidates {
		if err := journal.RejectAppliedDuplicates([]any{item}, recs); err != nil {
			return invalid(fmt.Sprintf("deep.proposal_candidates[%d]", i), err.Error())
		}
	}
	return nil
}

// rebuild makes the user's registry the projection of their published Deep reports onto recs:
// registry.Merge, which projects each proposal, then ValidateRegistry, references to other
// sessions checked for the session just published, then ReplaceForUser.
func (s *Service) rebuild(ctx context.Context, userID uuid.UUID, recs []journal.Record, publishedSession string) (int, error) {
	reports, err := s.published(ctx, userID)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]deepv2.Report, len(reports))
	for _, r := range reports {
		byID[r.SessionID] = deepv2.Report{Doc: r.Doc, Records: r.Records, CheckRefs: r.SessionID == publishedSession}
	}
	merged := registry.Merge(reports, recs)
	if err := deepv2.ValidateRegistry(merged, byID); err != nil {
		return 0, err //nolint:wrapcheck // the validator's text with its path is the answer
	}
	items, _ := merged["proposals"].([]any)
	proposals := make([]domain.Proposal, 0, len(items))
	for _, item := range items {
		p, _ := item.(map[string]any)
		text, err := journal.Canonical(p)
		if err != nil {
			return 0, fmt.Errorf("proposal document: %w", err)
		}
		id, _ := p["proposal_id"].(string)
		proposals = append(proposals, domain.Proposal{ProposalID: id, Document: text})
	}
	changed, err := s.proposals.ReplaceForUser(ctx, userID, proposals)
	if err != nil {
		return 0, fmt.Errorf("replace registry: %w", err)
	}
	return changed, nil
}

// published reads the published Deep reports of the user with userID for registry.Merge.
func (s *Service) published(ctx context.Context, userID uuid.UUID) ([]registry.PublishedReport, error) {
	versions, err := s.deepReports.PublishedByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("read published Deep reports: %w", err)
	}
	reports := make([]registry.PublishedReport, 0, len(versions))
	for _, v := range versions {
		doc, err := deepv2.Decode(v.Document)
		if err != nil {
			return nil, fmt.Errorf("published Deep report %s: %w", v.ID, err)
		}
		reports = append(reports, registry.PublishedReport{SessionID: v.SessionID, Doc: doc, Records: v.SourceRecords})
	}
	return reports, nil
}

// Merged returns the proposals of the user with userID merged from their published Deep
// reports and not projected onto the journal: what proposal_fingerprint and the frozen
// proposal_sources of a new journal record are taken from (deep-review, «Расхождения», п. 8).
func (s *Service) Merged(ctx context.Context, userID uuid.UUID) ([]registry.Proposal, error) {
	reports, err := s.published(ctx, userID)
	if err != nil {
		return nil, err
	}
	items, _ := registry.Merge(reports, nil)["proposals"].([]any)
	out := make([]registry.Proposal, 0, len(items))
	for _, item := range items {
		if p, ok := item.(map[string]any); ok {
			out = append(out, p)
		}
	}
	return out, nil
}
