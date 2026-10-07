package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Proposal is one proposal of a user's registry (docs/specs/deep-review/deep-review.md,
// «Реестр как проекция»): ProposalID is its group_key, Document the proposal object.
type Proposal struct {
	ProposalID string
	Document   json.RawMessage
	UpdatedAt  time.Time
}

// ProposalFilter selects registry proposals; a zero field does not filter.
type ProposalFilter struct {
	UserID uuid.UUID
}

// UserProposal is a registry proposal with the user it belongs to.
type UserProposal struct {
	UserID uuid.UUID
	Proposal
}

// Statuses of a skill opportunity report version.
const (
	SkillReportCurrent    = "current"
	SkillReportSuperseded = "superseded"
)

// SkillOpportunityReport is one version of a user's skill opportunity report
// (deep-review.md, «Отчёт о возможностях skills»). CorpusSHA256 is the hash of the report's
// corpus, AnalyzedAt the report's own text.
type SkillOpportunityReport struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Document     json.RawMessage
	CorpusSHA256 string
	Inventory    json.RawMessage
	AnalyzedAt   string
	RecordedAt   time.Time
	Status       string
}

// Registry and skill report errors.
var (
	ErrProposalNotFound    = &Error{Code: "proposal_not_found", Message: "Предложение не найдено"}
	ErrSkillReportNotFound = &Error{Code: "skill_report_not_found", Message: "Отчёт о возможностях skills не найден"}
	// ErrForbidden: a decision, application or effect is recorded on a proposal only by its
	// owner; a proposal missing from the user's registry answers the same.
	ErrForbidden = &Error{Code: "forbidden", Message: "Записать решение может только владелец предложения"}
)
