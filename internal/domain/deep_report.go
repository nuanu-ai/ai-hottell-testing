package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Statuses of a Deep report version (docs/specs/deep-review/deep-review.md, «Протокол Deep»).
const (
	// DeepReportCandidate: the version passed the validator and awaits an independent review.
	DeepReportCandidate = "candidate"
	// DeepReportPublished: the last published version of the session; only it enters the registry.
	DeepReportPublished = "published"
	// DeepReportSuperseded: a previous candidate or a previous published version.
	DeepReportSuperseded = "superseded"
)

// Results of the server's informational check of a report's source_sha256 against the
// stored lines of the session; none of them stops publication.
const (
	SourceCheckMatched    = "matched"
	SourceCheckNotChecked = "not_checked"
	SourceCheckMismatch   = "mismatch"
)

// DeepReport is one version of the Deep report of a session submitted by a user.
// Document is the canonical JSON of the masked report, byte for byte as CandidateSHA256
// was computed from it.
type DeepReport struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	SessionID       string
	Agent           string
	SourceSHA256    string
	SourceRecords   int
	CandidateSHA256 string
	Document        json.RawMessage
	Status          string
	SourceCheck     string
	SubmittedAt     time.Time
	// PublishedAt is nil until the version is published.
	PublishedAt *time.Time
}

// DeepReview is the independent semantic review that publishes a candidate: the review
// object as submitted, and its reviewer and reviewed_at texts.
type DeepReview struct {
	Review     json.RawMessage
	Reviewer   string
	ReviewedAt string
}

// DeepReportFilter selects Deep report versions; a zero field does not filter.
type DeepReportFilter struct {
	UserID uuid.UUID
	Agent  string
	Status string
	// From and To bound SubmittedAt as [From, To).
	From time.Time
	To   time.Time
}

// Deep report errors.
var (
	ErrDeepReportNotFound     = &Error{Code: "deep_report_not_found", Message: "Отчёт Deep не найден"}
	ErrDeepReportNotCandidate = &Error{Code: "deep_report_not_candidate", Message: "Отчёт Deep уже не ждёт проверки"}
)
