package ioc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/deep"
	"git.alva.dev/alva/harness-telemetry/internal/application/journal"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
	jr "git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// deepReportReader reads the Deep report versions the retro tools show.
type deepReportReader interface {
	PublishedByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeepReport, error)
	Candidate(ctx context.Context, userID, id uuid.UUID) (domain.DeepReport, error)
}

// proposalReader reads the registry the retro and coach tools show.
type proposalReader interface {
	List(ctx context.Context, filter domain.ProposalFilter) ([]domain.UserProposal, error)
	Get(ctx context.Context, userID uuid.UUID, proposalID string) (domain.Proposal, error)
}

// deepTools gives the MCP server's retro, coach and registry tools the Deep and journal
// services: it implements mcp.Deep, mcp.Journal, mcp.Registry and mcp.SkillReports, turning the
// services' refusals into the codes of mcp.md «Инструменты разбора и журнала».
type deepTools struct {
	deep      *deep.Service
	journal   *journal.Service
	reports   deepReportReader
	proposals proposalReader
}

// The answers mcp.md gives for what the user does not have.
var (
	errNoCandidate = &mcp.ToolError{Code: mcp.CodeNotFound, Message: "candidate_id: no such candidate"}                           //nolint:gochecknoglobals // a fixed answer, never written
	errNoProposal  = &mcp.ToolError{Code: mcp.CodeNotFound, Message: "proposal_id: proposal id is not in the published registry"} //nolint:gochecknoglobals // a fixed answer, never written
)

// toolError is the answer to err of a service: a validator's refusal is invalid with its path,
// a missing or foreign candidate or proposal not_found, a candidate past its review conflict;
// anything else stays an internal error.
func toolError(err error) error {
	var invalid *deepv2.ValidationError
	switch {
	case errors.As(err, &invalid):
		return &mcp.ToolError{Code: mcp.CodeInvalid, Message: invalid.Error()}
	case errors.Is(err, domain.ErrDeepReportNotFound):
		return errNoCandidate
	case errors.Is(err, jr.ErrClientRefConflict):
		return &mcp.ToolError{Code: mcp.CodeConflict, Message: jr.ErrClientRefConflict.Error()}
	case errors.Is(err, domain.ErrDeepReportNotCandidate):
		return &mcp.ToolError{Code: mcp.CodeConflict, Message: "candidate_id: candidate is superseded"}
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrProposalNotFound):
		return errNoProposal
	default:
		return err
	}
}

// rfc3339 reads a time the journal and the registry keep as text; a text that is not one is
// the zero time.
func rfc3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// deepDocument is the part of a stored Deep v2 report the context reads.
type deepDocument struct {
	Tasks []mcp.DeepCorpusTask `json:"tasks"`
}

// Context reads the user's published reports and registry: the session's report as the
// previous one, the others as the corpus, the applied proposals and every proposal.
func (t deepTools) Context(ctx context.Context, userID uuid.UUID, sessionID string) (mcp.DeepContext, error) {
	versions, err := t.reports.PublishedByUser(ctx, userID)
	if err != nil {
		return mcp.DeepContext{}, fmt.Errorf("read published Deep reports: %w", err)
	}
	var out mcp.DeepContext
	for _, v := range versions {
		if sessionID != "" && v.SessionID == sessionID {
			var at time.Time
			if v.PublishedAt != nil {
				at = v.PublishedAt.UTC()
			}
			out.PreviousReport = &mcp.DeepPreviousReport{
				CandidateID: v.ID.String(), CandidateSHA256: v.CandidateSHA256, SourceSHA256: v.SourceSHA256,
				SourceRecords: v.SourceRecords, PublishedAt: at, Report: v.Document,
			}
			continue
		}
		var doc deepDocument
		if err := json.Unmarshal(v.Document, &doc); err != nil {
			return mcp.DeepContext{}, fmt.Errorf("published Deep report %s: %w", v.ID, err)
		}
		out.Corpus = append(out.Corpus, mcp.DeepCorpusReport{
			SessionID: v.SessionID, SourceSHA256: v.SourceSHA256, SourceRecords: v.SourceRecords,
			CandidateSHA256: v.CandidateSHA256, Tasks: nonNilTasks(doc.Tasks),
		})
	}
	sortCorpus(out.Corpus)
	docs, err := t.proposalDocs(ctx, userID)
	if err != nil {
		return mcp.DeepContext{}, err
	}
	for _, doc := range docs {
		applied, brief, err := contextProposal(doc)
		if err != nil {
			return mcp.DeepContext{}, err
		}
		out.Proposals = append(out.Proposals, brief)
		if applied != nil {
			out.Applied = append(out.Applied, *applied)
		}
	}
	return out, nil
}

func nonNilTasks(tasks []mcp.DeepCorpusTask) []mcp.DeepCorpusTask {
	if tasks == nil {
		return []mcp.DeepCorpusTask{}
	}
	return tasks
}

// sortCorpus orders the corpus by session_id, as mcp.md gives it.
func sortCorpus(corpus []mcp.DeepCorpusReport) {
	slices.SortFunc(corpus, func(a, b mcp.DeepCorpusReport) int { return strings.Compare(a.SessionID, b.SessionID) })
}

// registryProposal is the part of a projected registry proposal the context reads.
type registryProposal struct {
	ProposalID   string          `json:"proposal_id"`
	GroupKey     string          `json:"group_key"`
	PatternID    string          `json:"pattern_id"`
	Scope        string          `json:"scope"`
	ChangeIntent string          `json:"change_intent"`
	Target       json.RawMessage `json:"target"`
	Change       string          `json:"change"`
	Readiness    struct {
		Status string `json:"status"`
	} `json:"readiness"`
	Execution struct {
		Status string `json:"status"`
		At     string `json:"at"`
	} `json:"execution"`
}

// contextProposal is a registry proposal as deep_context lists it, and, when its change is
// applied, as an applied one with the fingerprint of that version.
func contextProposal(doc json.RawMessage) (*mcp.DeepApplied, mcp.DeepProposal, error) {
	var p registryProposal
	if err := json.Unmarshal(doc, &p); err != nil {
		return nil, mcp.DeepProposal{}, fmt.Errorf("registry proposal: %w", err)
	}
	brief := mcp.DeepProposal{
		ProposalID: p.ProposalID, PatternID: p.PatternID, Scope: p.Scope, ChangeIntent: p.ChangeIntent,
		Readiness: p.Readiness.Status,
	}
	if p.Execution.Status != "applied" {
		return nil, brief, nil
	}
	var target mcp.DeepTarget
	if err := json.Unmarshal(p.Target, &target); err != nil {
		return nil, brief, fmt.Errorf("registry proposal %s target: %w", p.ProposalID, err)
	}
	var targetValue any
	if err := json.Unmarshal(p.Target, &targetValue); err != nil {
		return nil, brief, fmt.Errorf("registry proposal %s target: %w", p.ProposalID, err)
	}
	fp, err := jr.ProposalFingerprint(p.GroupKey, targetValue, p.Change)
	if err != nil {
		return nil, brief, fmt.Errorf("registry proposal %s fingerprint: %w", p.ProposalID, err)
	}
	return &mcp.DeepApplied{
		ProposalID: p.ProposalID, Fingerprint: fp, Target: target, Change: p.Change, At: rfc3339(p.Execution.At),
	}, brief, nil
}

// Candidate reads one report version of the user.
func (t deepTools) Candidate(ctx context.Context, userID, candidateID uuid.UUID) (mcp.DeepCandidate, error) {
	c, err := t.reports.Candidate(ctx, userID, candidateID)
	if err != nil {
		return mcp.DeepCandidate{}, toolError(err)
	}
	return mcp.DeepCandidate{
		CandidateID: c.ID.String(), SessionID: c.SessionID, CandidateSHA256: c.CandidateSHA256, Status: c.Status,
		SourceSHA256: c.SourceSHA256, SourceRecords: c.SourceRecords, SourceCheck: c.SourceCheck,
		SubmittedAt: c.SubmittedAt, Report: c.Document,
	}, nil
}

// Submit keeps a candidate through the Deep service.
func (t deepTools) Submit(ctx context.Context, userID uuid.UUID, s mcp.DeepSubmission) (mcp.DeepSubmitted, error) {
	kept, err := t.deep.Submit(ctx, userID, deep.SubmitInput{
		SessionID: s.SessionID, Agent: s.Agent, Path: s.Path, SourceSHA256: s.SourceSHA256,
		SourceRecords: s.SourceRecords, Report: s.Report,
	})
	if err != nil {
		return mcp.DeepSubmitted{}, toolError(err)
	}
	return mcp.DeepSubmitted{
		CandidateID: kept.ID.String(), CandidateSHA256: kept.CandidateSHA256, Status: kept.Status,
		SourceCheck: kept.SourceCheck,
	}, nil
}

// Review publishes a candidate through the Deep service; the service counts the registry's
// changes inside the publication's transaction, so a repeat reports none.
func (t deepTools) Review(
	ctx context.Context, userID, candidateID uuid.UUID, review json.RawMessage,
) (mcp.DeepPublished, error) {
	published, changed, err := t.deep.Review(ctx, userID, candidateID, review)
	if err != nil {
		return mcp.DeepPublished{}, toolError(err)
	}
	return mcp.DeepPublished{
		Status: domain.DeepReportPublished, CandidateID: published.ID.String(),
		CandidateSHA256: published.CandidateSHA256, ProposalsChanged: changed,
	}, nil
}

// proposalDocs reads the user's registry by proposal id.
func (t deepTools) proposalDocs(ctx context.Context, userID uuid.UUID) (map[string]json.RawMessage, error) {
	list, err := t.proposals.List(ctx, domain.ProposalFilter{UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	docs := make(map[string]json.RawMessage, len(list))
	for _, p := range list {
		if p.UserID == userID {
			docs[p.ProposalID] = p.Document
		}
	}
	return docs, nil
}

// Proposals reads the user's registry.
func (t deepTools) Proposals(ctx context.Context, userID uuid.UUID) ([]json.RawMessage, error) {
	list, err := t.proposals.List(ctx, domain.ProposalFilter{UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	docs := make([]json.RawMessage, 0, len(list))
	for _, p := range list {
		if p.UserID == userID {
			docs = append(docs, p.Document)
		}
	}
	return docs, nil
}

// Proposal reads one proposal of the user's registry.
func (t deepTools) Proposal(ctx context.Context, userID uuid.UUID, proposalID string) (json.RawMessage, error) {
	p, err := t.proposals.Get(ctx, userID, proposalID)
	if err != nil {
		return nil, toolError(err)
	}
	return p.Document, nil
}

// CoachRead reads the user's coach decisions with their checks folded.
func (t deepTools) CoachRead(ctx context.Context, userID uuid.UUID, filter mcp.CoachFilter) ([]mcp.CoachEntry, error) {
	views, err := t.journal.ReadCoach(ctx, userID, filter.ID, filter.TopicKey)
	if err != nil {
		return nil, toolError(err)
	}
	out := make([]mcp.CoachEntry, 0, len(views))
	for _, v := range views {
		e := coachEntry(v.Entry)
		if r := v.Recurrence; r != nil {
			result := r.Result
			at := rfc3339(r.CheckedAt)
			e.Result, e.Observations, e.Repeats, e.CheckedAt, e.Checks = &result, r.Observations, r.Repeats, &at, r.Checks
		}
		out = append(out, e)
	}
	return out, nil
}

// CoachAppend writes a coach record through the journal service. client_ref goes with the
// entry: a repeat with a key already written returns that record and writes nothing, the key
// with other content is a conflict (mcp.md «Идемпотентность append»).
func (t deepTools) CoachAppend(ctx context.Context, userID uuid.UUID, entry json.RawMessage) (mcp.CoachEntry, error) {
	raw, err := decodeObject(entry, "entry")
	if err != nil {
		return mcp.CoachEntry{}, err
	}
	written, err := t.journal.AppendCoach(ctx, userID, raw)
	if err != nil {
		return mcp.CoachEntry{}, toolError(err)
	}
	return coachEntry(written), nil
}

// decodeObject reads a free object of the arguments with deepv2.Decode, its numbers as written.
func decodeObject(data json.RawMessage, path string) (map[string]any, error) {
	v, err := deepv2.Decode(data)
	m, ok := v.(map[string]any)
	if err != nil || !ok {
		return nil, &mcp.ToolError{Code: mcp.CodeInvalid, Message: path + ": expected an object"}
	}
	return m, nil
}

// coachEntry is a journal coach entry as coach_journal answers it.
func coachEntry(e jr.CoachEntry) mcp.CoachEntry {
	evidence := make([]mcp.CoachEvidence, 0, len(e.Evidence))
	for _, ev := range e.Evidence {
		item := mcp.CoachEvidence{Session: ev.Session, Seq: ev.Seq, Quote: ev.Quote}
		if ev.At != "" {
			at := rfc3339(ev.At)
			item.At = &at
		}
		evidence = append(evidence, item)
	}
	if len(evidence) == 0 {
		evidence = nil
	}
	return mcp.CoachEntry{
		ID: e.ID, ClientRef: e.ClientRef, CheckOf: e.CheckOf, TopicKey: e.TopicKey, At: rfc3339(e.At), Agent: e.Agent, Topic: e.Topic,
		Findings: e.Findings, Evidence: evidence, Decision: e.Decision, Layer: e.Layer, Target: e.Target,
		BeforeSHA256: e.BeforeSHA256, AfterSHA256: e.AfterSHA256, Change: e.Change, Rollback: e.Rollback,
		Check: e.Check, CheckAfter: e.CheckAfter, Result: e.Result, Observations: e.Observations, Repeats: e.Repeats,
	}
}

// ProposalEvent writes an application or an effect through the journal service.
func (t deepTools) ProposalEvent(ctx context.Context, userID uuid.UUID, e mcp.ProposalEvent) (mcp.ProposalEventRecord, error) {
	detail, err := decodeObject(e.Detail, "detail")
	if err != nil {
		return mcp.ProposalEventRecord{}, err
	}
	in := journal.EventInput{ProposalID: e.ProposalID, AuthorityRef: e.AuthorityRef, Detail: detail}
	var r jr.Record
	if e.Event == jr.KindApplication {
		r, err = t.journal.AppendApplication(ctx, userID, in)
	} else {
		r, err = t.journal.AppendEffect(ctx, userID, in)
	}
	if err != nil {
		return mcp.ProposalEventRecord{}, toolError(err)
	}
	return mcp.ProposalEventRecord{
		RecordID: r.RecordID, Kind: r.Kind, RecordedAt: rfc3339(r.RecordedAt), ProposalID: r.ProposalID,
		ProposalFingerprint: r.ProposalFingerprint, RecordHash: r.RecordHash,
	}, nil
}

// skillTools gives skill_opportunities_submit the Deep service's skill reports.
type skillTools struct {
	deep *deep.Service
}

// Submit keeps a skill opportunities report through the Deep service.
func (t skillTools) Submit(ctx context.Context, userID uuid.UUID, report, inventory json.RawMessage) (mcp.SkillReportSubmitted, error) {
	kept, err := t.deep.SubmitSkills(ctx, userID, report, inventory)
	if err != nil {
		return mcp.SkillReportSubmitted{}, toolError(err)
	}
	return mcp.SkillReportSubmitted{
		Status: kept.Report.Status, ReportID: kept.Report.ID.String(), ReportSHA256: kept.SHA256,
		SubmittedAt: kept.Report.RecordedAt,
	}, nil
}
