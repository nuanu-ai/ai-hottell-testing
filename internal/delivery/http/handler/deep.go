package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/deep"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
	jr "git.alva.dev/alva/harness-telemetry/internal/domain/journal"
	"git.alva.dev/alva/harness-telemetry/internal/domain/registry"
	"git.alva.dev/alva/harness-telemetry/pkg/deepv2"
)

// Review is the Deep review, the proposal registry and the decision journal as the web reads
// them: every signed-in user reads every user's, only the owner decides on a proposal.
type Review interface {
	// DeepReports returns the report versions filter selects.
	DeepReports(ctx context.Context, filter domain.DeepReportFilter) ([]domain.DeepReport, error)
	// Proposals returns the registry proposals filter selects.
	Proposals(ctx context.Context, filter domain.ProposalFilter) ([]domain.UserProposal, error)
	// Proposal returns one proposal of userID's registry; domain.ErrProposalNotFound when none.
	Proposal(ctx context.Context, userID uuid.UUID, proposalID string) (domain.Proposal, error)
	// Decide records the owner's decision status on their proposal, with authorityRef.
	Decide(ctx context.Context, userID uuid.UUID, proposalID, status, authorityRef string) error
	// JournalRecords returns the journal of userID, or the whole journal for uuid.Nil.
	JournalRecords(ctx context.Context, userID uuid.UUID) ([]jr.Record, error)
	// VerifyJournal checks the hash chain: how many records hold and, when it is broken, the
	// seq of the first record that does not (0 when it holds).
	VerifyJournal(ctx context.Context) (records int, brokenAt int64, err error)
	// Skills returns the current skill opportunities report of userID;
	// domain.ErrSkillReportNotFound when they have none.
	Skills(ctx context.Context, userID uuid.UUID) (deep.SkillReportView, error)
	// UserNames names every user.
	UserNames(ctx context.Context) (map[uuid.UUID]string, error)
}

// WithReview gives the API the Deep review, the registry and the journal; without it their
// operations answer 503 analytics_unavailable.
func (a *API) WithReview(r Review) *API {
	a.review = r
	return a
}

// reviewReady checks that a user is signed in and the review is wired, and returns the user.
func (a *API) reviewReady(ctx context.Context) (uuid.UUID, error) {
	userID, _, err := signedIn(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if a.review == nil {
		return uuid.Nil, domain.ErrAnalyticsUnavailable
	}
	return userID, nil
}

// deepDoc is the part of a Deep v2 report the list reads.
type deepDoc struct {
	Tasks []struct {
		Outcome string `json:"outcome"`
	} `json:"tasks"`
	Checks []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"checks"`
}

// ListDeepReports answers the published Deep reports, the newest first.
func (a *API) ListDeepReports(
	ctx context.Context, request openapi.ListDeepReportsRequestObject,
) (openapi.ListDeepReportsResponseObject, error) {
	if _, err := a.reviewReady(ctx); err != nil {
		return nil, err
	}
	p := request.Params
	filter := domain.DeepReportFilter{Status: domain.DeepReportPublished}
	if p.User != nil {
		filter.UserID = *p.User
	}
	if p.Agent != nil {
		filter.Agent = string(*p.Agent)
	}
	versions, err := a.review.DeepReports(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("read Deep reports: %w", err)
	}
	names, err := a.review.UserNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("name users: %w", err)
	}
	reports := make([]openapi.DeepReportSummary, 0, len(versions))
	for _, v := range versions {
		if v.PublishedAt == nil || !inPeriod(*v.PublishedAt, p.From, p.To) {
			continue
		}
		summary, err := deepSummary(v, names[v.UserID])
		if err != nil {
			return nil, err
		}
		reports = append(reports, summary)
	}
	slices.SortStableFunc(reports, func(x, y openapi.DeepReportSummary) int { return y.PublishedAt.Compare(x.PublishedAt) })
	return openapi.ListDeepReports200JSONResponse{Reports: reports}, nil
}

// inPeriod reports whether t is in [from, to); an absent bound does not bound.
func inPeriod(t time.Time, from, to *time.Time) bool {
	return (from == nil || !t.Before(*from)) && (to == nil || t.Before(*to))
}

func deepSummary(v domain.DeepReport, name string) (openapi.DeepReportSummary, error) {
	var doc deepDoc
	if err := json.Unmarshal(v.Document, &doc); err != nil {
		return openapi.DeepReportSummary{}, fmt.Errorf("deep report %s: %w", v.ID, err)
	}
	outcomes := map[string]int{}
	for _, t := range doc.Tasks {
		outcomes[t.Outcome]++
	}
	suspected := []string{}
	for _, c := range doc.Checks {
		if c.Status == "suspected" || c.Status == "confirmed" {
			suspected = append(suspected, c.ID)
		}
	}
	return openapi.DeepReportSummary{
		SessionId: v.SessionID, UserId: v.UserID, UserName: name, Agent: openapi.AnalyticsAgent(v.Agent),
		PublishedAt: v.PublishedAt.UTC(), TasksCount: len(doc.Tasks), Outcomes: outcomes, SuspectedChecks: suspected,
	}, nil
}

// GetDeepReport answers the published report of a session and its newest unchecked candidate.
// A session of several people or agents is ambiguous until user and agent narrow it.
func (a *API) GetDeepReport(
	ctx context.Context, request openapi.GetDeepReportRequestObject,
) (openapi.GetDeepReportResponseObject, error) {
	if _, err := a.reviewReady(ctx); err != nil {
		return nil, err
	}
	filter := domain.DeepReportFilter{}
	if u := request.Params.User; u != nil {
		filter.UserID = *u
	}
	if ag := request.Params.Agent; ag != nil {
		filter.Agent = string(*ag)
	}
	versions, err := a.review.DeepReports(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("read Deep reports: %w", err)
	}
	var own []domain.DeepReport
	owners := map[string]bool{}
	for _, v := range versions {
		if v.SessionID == request.SessionId {
			own = append(own, v)
			owners[v.UserID.String()+"/"+v.Agent] = true
		}
	}
	if len(own) == 0 {
		return nil, domain.ErrDeepReportNotFound
	}
	if len(owners) > 1 {
		return nil, domain.ErrAnalyticsSessionAmbiguous
	}
	names, err := a.review.UserNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("name users: %w", err)
	}
	first := own[0]
	out := openapi.GetDeepReport200JSONResponse{
		SessionId: first.SessionID, UserId: first.UserID, UserName: names[first.UserID],
		Agent: openapi.AnalyticsAgent(first.Agent),
	}
	var published *domain.DeepReport
	for i, v := range own {
		if v.Status == domain.DeepReportPublished {
			published = &own[i]
		}
	}
	if published != nil {
		doc := map[string]any{}
		if err := json.Unmarshal(published.Document, &doc); err != nil {
			return nil, fmt.Errorf("deep report %s: %w", published.ID, err)
		}
		at := published.SubmittedAt
		if published.PublishedAt != nil {
			at = *published.PublishedAt
		}
		out.Published = &openapi.DeepPublished{
			CandidateSha256: published.CandidateSHA256, PublishedAt: at.UTC(), SourceSha256: published.SourceSHA256, Deep: doc,
		}
	}
	for _, v := range own {
		if v.Status != domain.DeepReportCandidate || (out.Candidate != nil && !v.SubmittedAt.After(out.Candidate.SubmittedAt)) {
			continue
		}
		out.Candidate = &openapi.DeepCandidate{
			CandidateSha256: v.CandidateSHA256, Status: openapi.DeepCandidateStatusCandidate, SubmittedAt: v.SubmittedAt.UTC(),
		}
	}
	return out, nil
}

// proposalDoc is a projected registry proposal (deep-review «Предложение»).
type proposalDoc struct {
	ProposalID     string                   `json:"proposal_id"`
	PatternID      string                   `json:"pattern_id"`
	Priority       string                   `json:"priority"`
	GroupKey       string                   `json:"group_key"`
	Action         string                   `json:"action"`
	ChangeType     string                   `json:"change_type"`
	Cause          string                   `json:"cause"`
	Change         string                   `json:"change"`
	Verification   string                   `json:"verification"`
	Rollback       string                   `json:"rollback"`
	ExpectedEffect string                   `json:"expected_effect"`
	Preconditions  []string                 `json:"preconditions"`
	Target         openapi.ProposalTarget   `json:"target"`
	Sources        []openapi.ProposalSource `json:"sources"`
	HistoryReview  string                   `json:"history_review"`
	Readiness      struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"readiness"`
	Decision struct {
		Status string `json:"status"`
	} `json:"decision"`
	Execution  openapi.ProposalExecution `json:"execution"`
	Effect     openapi.ProposalEffect    `json:"effect"`
	Recurrence *struct {
		Result       string `json:"result"`
		Observations *int   `json:"observations"`
		Repeats      *int   `json:"repeats"`
		CheckedAt    string `json:"checked_at"`
		Checks       int    `json:"checks"`
	} `json:"recurrence"`
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// reportKey names the Deep reports of one user's session.
type reportKey struct {
	user    uuid.UUID
	session string
}

// sourceReports is the decoded documents of the published Deep reports of userID, or of every
// user when it is uuid.Nil, by user and session: the newest published version of each agent's
// report of a session, the reports a proposal's sources point at.
func (a *API) sourceReports(ctx context.Context, userID uuid.UUID) (map[reportKey][]any, error) {
	versions, err := a.review.DeepReports(ctx, domain.DeepReportFilter{UserID: userID, Status: domain.DeepReportPublished})
	if err != nil {
		return nil, fmt.Errorf("read Deep reports: %w", err)
	}
	type agentKey struct {
		reportKey
		agent string
	}
	newest := map[agentKey]domain.DeepReport{}
	for _, v := range versions {
		k := agentKey{reportKey{v.UserID, v.SessionID}, v.Agent}
		if cur, ok := newest[k]; !ok || publishedAt(v).After(publishedAt(cur)) {
			newest[k] = v
		}
	}
	out := map[reportKey][]any{}
	for k, v := range newest {
		var doc any
		if err := json.Unmarshal(v.Document, &doc); err != nil {
			return nil, fmt.Errorf("deep report %s: %w", v.ID, err)
		}
		out[k.reportKey] = append(out[k.reportKey], doc)
	}
	return out, nil
}

func publishedAt(v domain.DeepReport) time.Time {
	if v.PublishedAt != nil {
		return *v.PublishedAt
	}
	return v.SubmittedAt
}

// proposalBody is a registry proposal as the web shows it; reports are the published Deep
// reports its severity is read from (sourceReports).
func proposalBody(userID uuid.UUID, name string, document []byte, reports map[reportKey][]any) (openapi.Proposal, error) {
	var d proposalDoc
	if err := json.Unmarshal(document, &d); err != nil {
		return openapi.Proposal{}, fmt.Errorf("registry proposal: %w", err)
	}
	history := []string{}
	for _, line := range strings.Split(d.HistoryReview, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			history = append(history, line)
		}
	}
	if d.Execution.Evidence == nil {
		d.Execution.Evidence = []string{}
	}
	out := openapi.Proposal{
		Id: d.ProposalID, UserId: userID, UserName: name, GroupKey: d.GroupKey, Title: textOrNil(d.Action),
		Kind: d.ChangeType, Target: d.Target, Change: textOrNil(d.Change), Cause: textOrNil(d.Cause),
		Preconditions: orEmpty(d.Preconditions), Verification: textOrNil(d.Verification), Rollback: textOrNil(d.Rollback),
		ExpectedEffect: textOrNil(d.ExpectedEffect), Sources: d.Sources, HistoryReview: history,
		Axes: openapi.ProposalAxes{
			Readiness: openapi.ProposalReadiness(d.Readiness.Status), ReadinessReason: textOrNil(d.Readiness.Reason),
			Decision:  openapi.ProposalDecision{Status: openapi.ProposalDecisionStatus(d.Decision.Status)},
			Execution: d.Execution, Effect: d.Effect,
		},
	}
	if out.Sources == nil {
		out.Sources = []openapi.ProposalSource{}
	}
	var sourceDocs []any
	seen := map[string]bool{}
	for _, src := range out.Sources {
		if !seen[src.SessionId] {
			seen[src.SessionId] = true
			sourceDocs = append(sourceDocs, reports[reportKey{userID, src.SessionId}]...)
		}
	}
	out.Severity = openapi.AnalyticsSeverity(registry.Severity(
		registry.Proposal{"pattern_id": d.PatternID, "priority": d.Priority}, sourceDocs))
	if r := d.Recurrence; r != nil {
		at, _ := time.Parse(time.RFC3339, r.CheckedAt)
		out.Recurrence = &openapi.ProposalRecurrence{
			Result: openapi.ProposalRecurrenceResult(r.Result), Observations: r.Observations, Repeats: r.Repeats,
			CheckedAt: at.UTC(), Checks: r.Checks,
		}
	}
	return out, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ListProposals answers the registries of every user, or of one, narrowed by readiness and
// decision.
func (a *API) ListProposals(
	ctx context.Context, request openapi.ListProposalsRequestObject,
) (openapi.ListProposalsResponseObject, error) {
	if _, err := a.reviewReady(ctx); err != nil {
		return nil, err
	}
	p := request.Params
	filter := domain.ProposalFilter{}
	if p.User != nil {
		filter.UserID = *p.User
	}
	list, err := a.review.Proposals(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	names, err := a.review.UserNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("name users: %w", err)
	}
	reports, err := a.sourceReports(ctx, filter.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.Proposal, 0, len(list))
	for _, item := range list {
		body, err := proposalBody(item.UserID, names[item.UserID], item.Document, reports)
		if err != nil {
			return nil, err
		}
		if (p.Readiness != nil && body.Axes.Readiness != *p.Readiness) ||
			(p.Decision != nil && body.Axes.Decision.Status != *p.Decision) {
			continue
		}
		out = append(out, body)
	}
	return openapi.ListProposals200JSONResponse{Proposals: out}, nil
}

// GetProposal answers one proposal of a user's registry.
func (a *API) GetProposal(
	ctx context.Context, request openapi.GetProposalRequestObject,
) (openapi.GetProposalResponseObject, error) {
	if _, err := a.reviewReady(ctx); err != nil {
		return nil, err
	}
	body, err := a.proposal(ctx, request.UserId, request.ProposalId)
	if err != nil {
		return nil, err
	}
	return openapi.GetProposal200JSONResponse(body), nil
}

func (a *API) proposal(ctx context.Context, userID uuid.UUID, proposalID string) (openapi.Proposal, error) {
	p, err := a.review.Proposal(ctx, userID, proposalID)
	if err != nil {
		return openapi.Proposal{}, err //nolint:wrapcheck // a domain error picks the status
	}
	names, err := a.review.UserNames(ctx)
	if err != nil {
		return openapi.Proposal{}, fmt.Errorf("name users: %w", err)
	}
	reports, err := a.sourceReports(ctx, userID)
	if err != nil {
		return openapi.Proposal{}, err
	}
	return proposalBody(userID, names[userID], p.Document, reports)
}

// codeDecisionAfterApplication answers a decision on a proposal already applied.
const codeDecisionAfterApplication = "decision_after_application"

// DecideProposal records the owner's decision on their proposal (authority_ref web:<user id>)
// and answers the proposal as the registry now projects it. Another user's registry is 403;
// a decision after the application 409; a decision the journal refuses otherwise 400.
func (a *API) DecideProposal(
	ctx context.Context, request openapi.DecideProposalRequestObject,
) (openapi.DecideProposalResponseObject, error) {
	userID, err := a.reviewReady(ctx)
	if err != nil {
		return nil, err
	}
	if request.UserId != userID {
		return nil, domain.ErrForbidden
	}
	if request.Body == nil {
		return openapi.DecideProposaldefaultJSONResponse{
			StatusCode: http.StatusBadRequest, Body: openapi.Error{Code: "invalid", Message: "Нет решения"},
		}, nil
	}
	err = a.review.Decide(ctx, userID, request.ProposalId, string(request.Body.Status), "web:"+userID.String())
	var refused *deepv2.ValidationError
	switch {
	case errors.As(err, &refused) && strings.Contains(refused.Reason, "after application"):
		return openapi.DecideProposaldefaultJSONResponse{
			StatusCode: http.StatusConflict,
			Body:       openapi.Error{Code: codeDecisionAfterApplication, Message: "Решение после применения не меняется"},
		}, nil
	case errors.As(err, &refused):
		return openapi.DecideProposaldefaultJSONResponse{
			StatusCode: http.StatusBadRequest, Body: openapi.Error{Code: "invalid", Message: refused.Error()},
		}, nil
	case err != nil:
		return nil, err //nolint:wrapcheck // a domain error picks the status
	}
	body, err := a.proposal(ctx, userID, request.ProposalId)
	if err != nil {
		return nil, err
	}
	return openapi.DecideProposal201JSONResponse(body), nil
}

// GetJournal answers the journal of the period, folded and newest first, with steps 4–6 of
// the improvement cycle.
func (a *API) GetJournal(
	ctx context.Context, request openapi.GetJournalRequestObject,
) (openapi.GetJournalResponseObject, error) {
	if _, err := a.reviewReady(ctx); err != nil {
		return nil, err
	}
	p := request.Params
	userID := uuid.Nil
	if p.User != nil {
		userID = *p.User
	}
	recs, err := a.review.JournalRecords(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("read decision journal: %w", err)
	}
	names, err := a.review.UserNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("name users: %w", err)
	}
	folded := jr.FoldChecks(recs)
	entries := []openapi.JournalEntry{}
	for _, r := range recs {
		at, err := time.Parse(time.RFC3339, r.RecordedAt)
		if err != nil || r.Kind == jr.KindCoachCheck || !inPeriod(at, p.From, p.To) {
			continue
		}
		e, err := journalEntry(r, at, names, folded)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	slices.Reverse(entries)
	// Counters counts nothing at or after to: an open period ends after every record.
	var from time.Time
	to := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	if p.From != nil {
		from = *p.From
	}
	if p.To != nil {
		to = *p.To
	}
	c := jr.Counters(recs, from, to)
	return openapi.GetJournal200JSONResponse{
		Entries: entries,
		Cycle: openapi.ImprovementCycle{
			Discussed: c.Discussed, Applied: c.Implemented, Checked: c.Verified, CheckedOf: c.Implemented,
			Empty: len(entries) == 0,
		},
	}, nil
}

// journalEntry is a record as the web shows it: the masked canonical record and the fields the
// page reads, a coach decision with its checks folded in.
func journalEntry(r jr.Record, at time.Time, names map[uuid.UUID]string, folded map[string]jr.Recurrence) (openapi.JournalEntry, error) {
	text, err := r.CanonicalText()
	if err != nil {
		return openapi.JournalEntry{}, fmt.Errorf("journal record %s: %w", r.RecordID, err)
	}
	record := map[string]any{}
	if err := json.Unmarshal(text, &record); err != nil {
		return openapi.JournalEntry{}, fmt.Errorf("journal record %s: %w", r.RecordID, err)
	}
	userID, _ := uuid.Parse(r.UserID)
	recordID, _ := uuid.Parse(r.RecordID)
	e := openapi.JournalEntry{
		RecordId: recordID, Kind: openapi.JournalEntryKind(r.Kind), RecordedAt: at.UTC(), UserId: userID,
		UserName: names[userID], ProposalId: textOrNil(r.ProposalID), Record: record,
	}
	if r.Coach != nil {
		e.TopicKey = textOrNil(r.Coach.TopicKey)
		e.Decision = textOrNil(r.Coach.Decision)
	} else if status, ok := r.Detail["status"].(string); ok {
		e.Decision = &status
	}
	if rec, ok := folded[r.RecordID]; ok {
		result := openapi.JournalEntryResult(rec.Result)
		checks := rec.Checks
		e.Result, e.Observations, e.Repeats, e.Checks = &result, rec.Observations, rec.Repeats, &checks
		if t, err := time.Parse(time.RFC3339, rec.CheckedAt); err == nil {
			t = t.UTC()
			e.CheckedAt = &t
		}
	}
	return e, nil
}

// VerifyJournal answers whether the hash chain of the decision journal holds.
func (a *API) VerifyJournal(
	ctx context.Context, _ openapi.VerifyJournalRequestObject,
) (openapi.VerifyJournalResponseObject, error) {
	if _, err := a.reviewReady(ctx); err != nil {
		return nil, err
	}
	n, broken, err := a.review.VerifyJournal(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify decision journal: %w", err)
	}
	out := openapi.VerifyJournal200JSONResponse{Ok: broken == 0, Records: int64(n)}
	if broken != 0 {
		out.AtSeq = &broken
	}
	return out, nil
}

// GetSkillOpportunities answers the current skill opportunities report of a user, the signed-in
// one by default; report is absent when they have none.
func (a *API) GetSkillOpportunities(
	ctx context.Context, request openapi.GetSkillOpportunitiesRequestObject,
) (openapi.GetSkillOpportunitiesResponseObject, error) {
	userID, err := a.reviewReady(ctx)
	if err != nil {
		return nil, err
	}
	if request.Params.User != nil {
		userID = *request.Params.User
	}
	view, err := a.review.Skills(ctx, userID)
	if errors.Is(err, domain.ErrSkillReportNotFound) {
		return openapi.GetSkillOpportunities200JSONResponse{Stale: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skill report: %w", err)
	}
	report := map[string]any{}
	if err := json.Unmarshal(view.Report.Document, &report); err != nil {
		return nil, fmt.Errorf("skill report %s: %w", view.Report.ID, err)
	}
	at := view.Report.RecordedAt.UTC()
	return openapi.GetSkillOpportunities200JSONResponse{Stale: view.Stale, SubmittedAt: &at, Report: &report}, nil
}
