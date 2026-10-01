package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// V2 files are private, versioned analytical products. The dashboard never
// infers them from a transcript or treats them as recorded telemetry.
type V2DeepReport struct {
	Kind               string          `json:"kind"`
	SchemaVersion      int             `json:"schema_version"`
	SessionID          string          `json:"session_id"`
	SourceSHA256       string          `json:"source_sha256"`
	AnalysisVersion    string          `json:"analysis_version,omitempty"`
	PreviousReport     string          `json:"previous_report,omitempty"`
	Tasks              []V2Task        `json:"tasks"`
	Checks             []V2Check       `json:"checks"`
	Observations       []V2Observation `json:"observations"`
	ProposalCandidates []V2Proposal    `json:"proposal_candidates"`
	Unknowns           []string        `json:"unknowns"`
}

type V2Task struct {
	TaskID           string              `json:"task_id"`
	Goal             string              `json:"goal"`
	GoalEvidence     []string            `json:"goal_evidence"`
	Scope            string              `json:"scope"`
	StartLine        int                 `json:"start_line"`
	EndLine          int                 `json:"end_line"`
	BoundaryEvidence []string            `json:"boundary_evidence"`
	SuccessCriteria  []string            `json:"success_criteria"`
	Outcome          string              `json:"outcome"`
	OutcomeBasis     []string            `json:"outcome_basis"`
	Claims           []V2Claim           `json:"claims"`
	ContinuedFrom    *V2TaskContinuation `json:"continued_from,omitempty"`
}

type V2TaskContinuation struct {
	SessionID        string   `json:"session_id"`
	TaskID           string   `json:"task_id"`
	SourceSHA256     string   `json:"source_sha256"`
	CurrentEvidence  []string `json:"current_evidence"`
	PreviousEvidence []string `json:"previous_evidence"`
}

type V2Claim struct {
	Line                 int      `json:"line"`
	Claim                string   `json:"claim"`
	VerificationAtClaim  string   `json:"verification_at_claim"`
	Evidence             []string `json:"evidence"`
	SubsequentResolution string   `json:"subsequent_resolution"`
}

type V2Check struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	Evidence    []string   `json:"evidence"`
	MissingData []string   `json:"missing_data"`
	TaskIDs     []string   `json:"task_ids"`
	PeerSources []V2Source `json:"peer_sources,omitempty"`
}

type V2Observation struct {
	TaskIDs  []string `json:"task_ids"`
	Pattern  string   `json:"pattern"`
	Finding  string   `json:"finding"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

type V2ProposalRegistry struct {
	Kind          string       `json:"kind"`
	SchemaVersion int          `json:"schema_version"`
	Proposals     []V2Proposal `json:"proposals"`
}

type V2Proposal struct {
	ProposalID        string         `json:"proposal_id"`
	GroupKey          string         `json:"group_key"`
	Action            string         `json:"action"`
	Observed          string         `json:"observed"`
	Cause             string         `json:"cause"`
	ExistingRule      V2ExistingRule `json:"existing_rule"`
	ChangeType        string         `json:"change_type"`
	Scope             string         `json:"scope"`
	Target            V2Target       `json:"target"`
	Change            string         `json:"change"`
	Verification      string         `json:"verification"`
	Rollback          string         `json:"rollback"`
	Readiness         V2State        `json:"readiness"`
	Decision          V2State        `json:"decision"`
	Execution         V2Execution    `json:"execution"`
	Effect            V2Effect       `json:"effect"`
	Sources           []V2Source     `json:"sources"`
	Priority          string         `json:"priority,omitempty"`
	PatternID         string         `json:"pattern_id,omitempty"`
	ChangeIntent      string         `json:"change_intent,omitempty"`
	SelectionReason   string         `json:"selection_reason,omitempty"`
	ExpectedEffect    string         `json:"expected_effect,omitempty"`
	AlternativeCauses []string       `json:"alternative_causes,omitempty"`
	Preconditions     []string       `json:"preconditions,omitempty"`
	Exceptions        []string       `json:"exceptions,omitempty"`
	AnalysisVersions  []string       `json:"analysis_versions,omitempty"`
	HistoryReview     string         `json:"history_review,omitempty"`
	AutomationBasis   *V2Automation  `json:"automation_basis,omitempty"`
}

type V2Automation struct {
	Steps                      []string `json:"steps"`
	Frequency                  string   `json:"frequency"`
	Benefit                    string   `json:"benefit"`
	ExistingAutomationsChecked string   `json:"existing_automations_checked"`
	ExceptionHandling          string   `json:"exception_handling"`
	PermissionScope            string   `json:"permission_scope"`
	StopMethod                 string   `json:"stop_method"`
}

type V2ExistingRule struct {
	Status      string `json:"status"`
	Description string `json:"description"`
	Locator     string `json:"locator"`
	Version     string `json:"version"`
}

type V2Target struct {
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
	Version string `json:"version"`
}

type V2State struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	At     string `json:"at,omitempty"`
}

type V2Execution struct {
	Status   string   `json:"status"`
	At       string   `json:"at,omitempty"`
	Version  string   `json:"version,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

type V2Effect struct {
	Status  string `json:"status"`
	Method  string `json:"method,omitempty"`
	Metric  string `json:"metric,omitempty"`
	NBefore int    `json:"n_before,omitempty"`
	NAfter  int    `json:"n_after,omitempty"`
	Note    string `json:"note,omitempty"`
}

type V2Source struct {
	SessionID    string   `json:"session_id"`
	TaskID       string   `json:"task_id"`
	SourceSHA256 string   `json:"source_sha256"`
	Evidence     []string `json:"evidence"`
}

func (s *server) v2DeepPath(id string) string {
	return filepath.Join(s.dataDir, "v2", "deep", id+".json")
}

func (s *server) v2DeepExists(id string) bool {
	if !safeID.MatchString(id) {
		return false
	}
	_, err := os.Stat(s.v2DeepPath(id))
	return err == nil
}

func (s *server) v2Deep(id string) (V2DeepReport, error) {
	if !safeID.MatchString(id) {
		return V2DeepReport{}, os.ErrNotExist
	}
	f, err := os.Open(s.v2DeepPath(id))
	if err != nil {
		return V2DeepReport{}, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > 4<<20 {
		return V2DeepReport{}, fmt.Errorf("v2 deep exceeds size limit")
	}
	var report V2DeepReport
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	if err := decoder.Decode(&report); err != nil {
		return V2DeepReport{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return V2DeepReport{}, fmt.Errorf("v2 deep has trailing content")
	}
	if report.Kind != "deep" || report.SchemaVersion != 2 || report.SessionID != id || len(report.SourceSHA256) != 64 {
		return V2DeepReport{}, fmt.Errorf("invalid v2 deep identity")
	}
	if _, err := hex.DecodeString(report.SourceSHA256); err != nil {
		return V2DeepReport{}, fmt.Errorf("invalid v2 source hash: %w", err)
	}
	if len(report.Tasks) == 0 || len(report.Checks) != len(detectorCatalogue) {
		return V2DeepReport{}, fmt.Errorf("v2 deep requires tasks and all 13 checks")
	}
	taskIDs := make(map[string]bool, len(report.Tasks))
	for _, task := range report.Tasks {
		if task.TaskID == "" || task.Goal == "" || task.StartLine < 1 || task.EndLine < task.StartLine || len(task.GoalEvidence) == 0 || len(task.BoundaryEvidence) == 0 || taskIDs[task.TaskID] {
			return V2DeepReport{}, fmt.Errorf("invalid v2 task boundary")
		}
		if task.ContinuedFrom != nil {
			link := task.ContinuedFrom
			if link.SessionID == "" || link.SessionID == id || link.TaskID == "" || len(link.SourceSHA256) != 64 || len(link.CurrentEvidence) == 0 || len(link.PreviousEvidence) == 0 {
				return V2DeepReport{}, fmt.Errorf("invalid v2 task continuation")
			}
			if _, err := hex.DecodeString(link.SourceSHA256); err != nil {
				return V2DeepReport{}, fmt.Errorf("invalid v2 task continuation source hash: %w", err)
			}
		}
		switch task.Outcome {
		case "unknown", "verified", "partial", "failed":
		default:
			return V2DeepReport{}, fmt.Errorf("invalid v2 task outcome")
		}
		taskIDs[task.TaskID] = true
		for _, claim := range task.Claims {
			if claim.Line < task.StartLine || claim.Line > task.EndLine || !oneOf(claim.VerificationAtClaim, "verified", "unverified", "contradicted", "unknown") || len(claim.Evidence) == 0 {
				return V2DeepReport{}, fmt.Errorf("invalid v2 completion claim")
			}
		}
	}
	for i, check := range report.Checks {
		if check.ID != detectorCatalogue[i].ID || check.Summary == "" {
			return V2DeepReport{}, fmt.Errorf("invalid v2 check %d", i)
		}
		switch check.Status {
		case "suspected", "checked_clear":
			if len(check.Evidence) == 0 {
				return V2DeepReport{}, fmt.Errorf("v2 check %s lacks evidence", check.ID)
			}
		case "insufficient_data":
			if len(check.MissingData) == 0 {
				return V2DeepReport{}, fmt.Errorf("v2 check %s lacks missing data", check.ID)
			}
		case "not_checked", "not_applicable":
		default:
			return V2DeepReport{}, fmt.Errorf("invalid v2 check status %s", check.Status)
		}
		for _, taskID := range check.TaskIDs {
			if !taskIDs[taskID] {
				return V2DeepReport{}, fmt.Errorf("v2 check references missing task %s", taskID)
			}
		}
		for _, peer := range check.PeerSources {
			if !safeID.MatchString(peer.SessionID) || peer.SessionID == "." || peer.SessionID == ".." || peer.SessionID == id || peer.TaskID == "" || len(peer.SourceSHA256) != 64 || len(peer.Evidence) == 0 {
				return V2DeepReport{}, fmt.Errorf("invalid v2 peer source in %s", check.ID)
			}
			if _, err := hex.DecodeString(peer.SourceSHA256); err != nil {
				return V2DeepReport{}, fmt.Errorf("invalid v2 peer source hash in %s", check.ID)
			}
		}
	}
	for _, observation := range report.Observations {
		if observation.Finding == "" || len(observation.Evidence) == 0 || len(observation.TaskIDs) == 0 {
			return V2DeepReport{}, fmt.Errorf("invalid v2 observation")
		}
		for _, taskID := range observation.TaskIDs {
			if !taskIDs[taskID] {
				return V2DeepReport{}, fmt.Errorf("v2 observation references missing task %s", taskID)
			}
		}
	}
	return report, nil
}

func (s *server) v2Registry() (V2ProposalRegistry, error) {
	f, err := os.Open(filepath.Join(s.dataDir, "v2", "proposals.json"))
	if err != nil {
		return V2ProposalRegistry{}, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > 4<<20 {
		return V2ProposalRegistry{}, fmt.Errorf("v2 registry exceeds size limit")
	}
	var registry V2ProposalRegistry
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	if err := decoder.Decode(&registry); err != nil {
		return V2ProposalRegistry{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return V2ProposalRegistry{}, fmt.Errorf("v2 registry has trailing content")
	}
	if registry.Kind != "proposal_registry" || registry.SchemaVersion != 2 {
		return V2ProposalRegistry{}, fmt.Errorf("invalid v2 proposal registry")
	}
	seen := map[string]bool{}
	groups := map[string]bool{}
	for _, p := range registry.Proposals {
		if p.ProposalID == "" || p.GroupKey == "" || p.Action == "" || seen[p.ProposalID] || groups[p.GroupKey] || len(p.Sources) == 0 {
			return V2ProposalRegistry{}, fmt.Errorf("invalid v2 proposal identity")
		}
		seen[p.ProposalID] = true
		groups[p.GroupKey] = true
		if !oneOf(p.Priority, "low", "medium", "high") || !oneOf(p.ExistingRule.Status, "found", "not_found", "not_checked") {
			return V2ProposalRegistry{}, fmt.Errorf("invalid v2 proposal classification")
		}
		if !oneOf(p.Readiness.Status, "hypothesis", "needs_specification", "prepared_verified") || !oneOf(p.Decision.Status, "not_requested", "accepted", "rejected", "revision_requested") || !oneOf(p.Execution.Status, "not_applied", "applied") || !oneOf(p.Effect.Status, "not_measured", "helped", "no_effect", "worse", "insufficient_data") {
			return V2ProposalRegistry{}, fmt.Errorf("invalid v2 proposal state")
		}
		if p.Execution.Status != "applied" && p.Effect.Status != "not_measured" {
			return V2ProposalRegistry{}, fmt.Errorf("effect cannot precede application")
		}
		if p.Execution.Status == "applied" && (p.Readiness.Status != "prepared_verified" || p.Decision.Status != "accepted") {
			return V2ProposalRegistry{}, fmt.Errorf("applied proposal lacks readiness or decision")
		}
	}
	return registry, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func sortedProposals(registry V2ProposalRegistry) []V2Proposal {
	proposals := append([]V2Proposal(nil), registry.Proposals...)
	sort.SliceStable(proposals, func(i, j int) bool {
		a, b := proposals[i], proposals[j]
		if a.Decision.Status == "rejected" && b.Decision.Status != "rejected" {
			return false
		}
		if b.Decision.Status == "rejected" && a.Decision.Status != "rejected" {
			return true
		}
		if a.Priority != b.Priority {
			return priorityRank(a.Priority) > priorityRank(b.Priority)
		}
		if len(a.Sources) != len(b.Sources) {
			return len(a.Sources) > len(b.Sources)
		}
		return a.ProposalID < b.ProposalID
	})
	return proposals
}

func priorityRank(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func (s *server) v2DeepAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.v2Deep(r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	_ = report
	data, err := os.ReadFile(s.v2DeepPath(r.PathValue("id")))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, json.RawMessage(data))
}

func (s *server) v2ProposalsAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.v2Registry()
	if err != nil {
		serveError(w, err)
		return
	}
	_ = report
	data, err := os.ReadFile(filepath.Join(s.dataDir, "v2", "proposals.json"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, json.RawMessage(data))
}

func isAbsent(err error) bool { return errors.Is(err, os.ErrNotExist) }

func joinedEvidence(items []string) string { return strings.Join(items, ", ") }
