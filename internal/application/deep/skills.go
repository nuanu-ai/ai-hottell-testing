package deep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// SkillSubmission is a skill opportunity report as kept: the version and the sha256 of its
// canonical masked text (report_sha256 of skill_opportunities_submit).
type SkillSubmission struct {
	Report domain.SkillOpportunityReport
	SHA256 string
}

// SkillReportView is the current skill report of a user and whether it is stale: the set of
// the user's published Deep reports, or the version of one of them, is no longer its corpus.
type SkillReportView struct {
	Report domain.SkillOpportunityReport
	Stale  bool
}

// SubmitSkills accepts the skill opportunity report of the user with userID
// (skill_opportunities_submit; deep-review spec, «Отчёт о возможностях skills»): report and
// inventory are JSON texts, inventory the skills_inventory snapshot the report must carry
// unchanged. Every string of both is masked first; the masked report is validated against
// the user's published Deep reports and kept, canonical, as the user's current report.
// A report the user already has, by its canonical text, is returned as is, current or
// superseded, and nothing is written. An invalid input is a *deepv2.ValidationError.
func (s *Service) SubmitSkills(ctx context.Context, userID uuid.UUID, report, inventory []byte) (SkillSubmission, error) {
	doc, err := decodeMasked(report, "skill report")
	if err != nil {
		return SkillSubmission{}, err
	}
	inv, err := decodeMasked(inventory, "inventory")
	if err != nil {
		return SkillSubmission{}, err
	}
	reportMap, _ := doc.(map[string]any)
	if !canonicalEqual(reportMap["inventory"], inv) {
		return SkillSubmission{}, invalid("inventory", "differs from recorded local snapshot")
	}
	text, err := journal.Canonical(doc)
	if err != nil {
		return SkillSubmission{}, invalid("skill report", "integer numbers only")
	}
	sum := journal.SHA256Hex(text)

	history, err := s.skillReports.History(ctx, userID)
	if err != nil {
		return SkillSubmission{}, fmt.Errorf("read skill reports: %w", err)
	}
	for _, kept := range history {
		if documentSHA256(kept.Document) == sum {
			return SkillSubmission{Report: kept, SHA256: sum}, nil
		}
	}

	published, err := s.publishedDeep(ctx, userID)
	if err != nil {
		return SkillSubmission{}, err
	}
	if err := deepv2.ValidateSkillReport(doc, published); err != nil {
		return SkillSubmission{}, err //nolint:wrapcheck // the validator's text is the answer
	}
	corpus, err := journal.Canonical(reportMap["corpus"])
	if err != nil {
		return SkillSubmission{}, invalid("corpus", "integer numbers only")
	}
	invText, err := journal.Canonical(inv)
	if err != nil {
		return SkillSubmission{}, invalid("inventory", "integer numbers only")
	}
	analyzedAt, _ := reportMap["analyzed_at"].(string)
	stored, err := s.skillReports.InsertCurrent(ctx, domain.SkillOpportunityReport{
		UserID: userID, Document: text, CorpusSHA256: journal.SHA256Hex(corpus), Inventory: invText,
		AnalyzedAt: analyzedAt,
	})
	if err != nil {
		return SkillSubmission{}, fmt.Errorf("store skill report: %w", err)
	}
	return SkillSubmission{Report: stored, SHA256: sum}, nil
}

// Skills returns the current skill report of the user with userID and whether it is stale;
// domain.ErrSkillReportNotFound when the user has none.
func (s *Service) Skills(ctx context.Context, userID uuid.UUID) (SkillReportView, error) {
	report, err := s.skillReports.Current(ctx, userID)
	if err != nil {
		return SkillReportView{}, fmt.Errorf("read skill report: %w", err)
	}
	reports, err := s.deepReports.PublishedByUser(ctx, userID)
	if err != nil {
		return SkillReportView{}, fmt.Errorf("read published Deep reports: %w", err)
	}
	current := make(map[string]string, len(reports))
	for _, r := range reports {
		current[r.SessionID] = r.CandidateSHA256
	}
	return SkillReportView{Report: report, Stale: !corpusIs(report.Document, current)}, nil
}

// corpusIs reports whether the corpus of the stored report document names exactly the
// sessions of current at their published candidate versions.
func corpusIs(document []byte, current map[string]string) bool {
	var doc struct {
		Corpus []struct {
			SessionID        string `json:"session_id"`
			DeepReportSHA256 string `json:"deep_report_sha256"`
		} `json:"corpus"`
	}
	if err := json.Unmarshal(document, &doc); err != nil || len(doc.Corpus) != len(current) {
		return false
	}
	for _, c := range doc.Corpus {
		if sum, ok := current[c.SessionID]; !ok || sum != c.DeepReportSHA256 {
			return false
		}
	}
	return true
}

// publishedDeep reads the published Deep reports of the user for the validators, by session.
func (s *Service) publishedDeep(ctx context.Context, userID uuid.UUID) (map[string]deepv2.PublishedDeep, error) {
	reports, err := s.deepReports.PublishedByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("read published Deep reports: %w", err)
	}
	out := make(map[string]deepv2.PublishedDeep, len(reports))
	for _, r := range reports {
		doc, err := deepv2.Decode(r.Document)
		if err != nil {
			return nil, fmt.Errorf("published Deep report %s: %w", r.ID, err)
		}
		out[r.SessionID] = deepv2.PublishedDeep{Doc: doc, CandidateSHA256: r.CandidateSHA256}
	}
	return out, nil
}

// canonicalEqual reports whether a and b have the same canonical JSON text.
func canonicalEqual(a, b any) bool {
	ta, errA := journal.Canonical(a)
	tb, errB := journal.Canonical(b)
	return errors.Join(errA, errB) == nil && bytes.Equal(ta, tb)
}

// documentSHA256 is the sha256 of the canonical text of a stored document, which jsonb gives
// back reformatted; "" when it cannot be read.
func documentSHA256(document []byte) string {
	v, err := deepv2.Decode(document)
	if err != nil {
		return ""
	}
	text, err := journal.Canonical(v)
	if err != nil {
		return ""
	}
	return journal.SHA256Hex(text)
}
