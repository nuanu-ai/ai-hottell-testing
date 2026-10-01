package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func v2Fixture(id string) V2DeepReport {
	checks := make([]V2Check, 0, len(detectorCatalogue))
	for _, detector := range detectorCatalogue {
		checks = append(checks, V2Check{ID: detector.ID, Status: "not_checked", Summary: "Ждёт разбора", Evidence: []string{}, MissingData: []string{}, TaskIDs: []string{}})
	}
	checks[2] = V2Check{ID: "D05", Status: "suspected", Summary: "Раннее заявление", Evidence: []string{"L2"}, MissingData: []string{}, TaskIDs: []string{"task-1"}}
	return V2DeepReport{Kind: "deep", SchemaVersion: 2, SessionID: id, SourceSHA256: strings.Repeat("a", 64), Tasks: []V2Task{{TaskID: "task-1", Goal: "Собрать два контейнера", GoalEvidence: []string{"L1"}, Scope: "локальный сервис", StartLine: 1, EndLine: 3, BoundaryEvidence: []string{"L1"}, SuccessCriteria: []string{"оба контейнера"}, Outcome: "partial", OutcomeBasis: []string{"L3"}, Claims: []V2Claim{{Line: 2, Claim: "Готово", VerificationAtClaim: "unverified", Evidence: []string{"L2"}, SubsequentResolution: "исправлено"}}}}, Checks: checks, Observations: []V2Observation{{Pattern: "D05", Finding: "Заявлено рано", Status: "suspected", Evidence: []string{"L2"}, TaskIDs: []string{"task-1"}}}, ProposalCandidates: []V2Proposal{}, Unknowns: []string{}}
}

func TestV2DeepTakesPrecedenceAndKeepsTaskBoundaries(t *testing.T) {
	dir := t.TempDir()
	id := "session-v2"
	report := v2Fixture(id)
	report.Tasks[0].ContinuedFrom = &V2TaskContinuation{SessionID: "previous-session", TaskID: "task-4", SourceSHA256: strings.Repeat("b", 64), CurrentEvidence: []string{"L1"}, PreviousEvidence: []string{"L8"}}
	writeJSONFixture(t, filepath.Join(dir, "v2", "deep", id+".json"), report)
	writeJSONFixture(t, filepath.Join(dir, "deep", id+".json"), DeepReport{Kind: "deep", SessionID: id, Task: "old task", Outcome: "unknown", OutcomeBasis: "old basis"})
	s := &server{dataDir: dir}
	if !s.deepExists(id) {
		t.Fatal("v2 deep not found")
	}
	coverage, err := s.coverage(id)
	if err != nil || coverage.SchemaVersion != 2 || coverage.Checks[2].Status != "suspected" {
		t.Fatalf("v2 coverage: %+v, %v", coverage, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/deep", nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.deepPage(w, req)
	body := w.Body.String()
	for _, want := range []string{"Глубокий отчёт 2.0", "Собрать два контейнера", "L1–L3", "Заявления о готовности", "на момент заявления", "Покрытие всех 13 проверок", "Продолжение задания", "/sessions/previous-session/deep"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in v2 page", want)
		}
	}
	if strings.Contains(body, "old task") {
		t.Error("legacy content displaced v2 analysis")
	}
	report.Tasks[0].BoundaryEvidence = nil
	writeJSONFixture(t, filepath.Join(dir, "v2", "deep", id+".json"), report)
	if _, err := s.v2Deep(id); err == nil {
		t.Fatal("accepted task without boundary evidence")
	}
}

func TestV2RegistryDoesNotDuplicateGroupOrClaimEffectBeforeApply(t *testing.T) {
	dir := t.TempDir()
	p := V2Proposal{ProposalID: "P1", GroupKey: "group-1", Action: "Проверить правило", Observed: "Повтор", ExistingRule: V2ExistingRule{Status: "not_checked"}, Priority: "high", Readiness: V2State{Status: "hypothesis", Reason: "адрес неизвестен"}, Decision: V2State{Status: "not_requested"}, Execution: V2Execution{Status: "not_applied"}, Effect: V2Effect{Status: "not_measured"}, Sources: []V2Source{{SessionID: "session-v2", TaskID: "task-1", SourceSHA256: strings.Repeat("a", 64), Evidence: []string{"L2"}}}}
	path := filepath.Join(dir, "v2", "proposals.json")
	writeJSONFixture(t, path, V2ProposalRegistry{Kind: "proposal_registry", SchemaVersion: 2, Proposals: []V2Proposal{p}})
	s := &server{dataDir: dir}
	if _, err := s.v2Registry(); err != nil {
		t.Fatal(err)
	}
	p.Effect.Status = "helped"
	writeJSONFixture(t, path, V2ProposalRegistry{Kind: "proposal_registry", SchemaVersion: 2, Proposals: []V2Proposal{p}})
	if _, err := s.v2Registry(); err == nil {
		t.Fatal("effect claimed before application")
	}
	p.Effect.Status = "not_measured"
	duplicate := p
	duplicate.ProposalID = "P2"
	writeJSONFixture(t, path, V2ProposalRegistry{Kind: "proposal_registry", SchemaVersion: 2, Proposals: []V2Proposal{p, duplicate}})
	if _, err := s.v2Registry(); err == nil {
		t.Fatal("duplicate proposal group shown twice")
	}
}

func TestSkillOpportunityPromptIsEscapedAndStaleCorpusShown(t *testing.T) {
	dir := t.TempDir()
	id := "session-v2"
	report := v2Fixture(id)
	writeJSONFixture(t, filepath.Join(dir, "v2", "deep", id+".json"), report)
	skill := SkillReport{Kind: "skill_opportunities", SchemaVersion: 1, AnalyzedAt: "2026-09-30T00:00:00Z", Corpus: []SkillCorpusEntry{{SessionID: id, SourceSHA256: strings.Repeat("b", 64)}}, Opportunities: []SkillOpportunity{{ID: "release-gate", Kind: "create", Title: "Gate", Recommendation: "Check", Readiness: "hypothesis", CopyPrompt: "</textarea><script>alert(1)</script>", Sources: []V2Source{{SessionID: id, TaskID: "task-1", Evidence: []string{"L2"}}}}}}
	writeJSONFixture(t, filepath.Join(dir, "v2", "skill-opportunities.json"), skill)
	s := &server{dataDir: dir}
	var body strings.Builder
	s.writeSkillOpportunities(&body)
	if !strings.Contains(body.String(), "требуют обновления") || !strings.Contains(body.String(), "Скопировать промпт") {
		t.Fatalf("staleness or copy affordance missing: %s", body.String())
	}
	if strings.Contains(body.String(), "</textarea><script>alert(1)</script>") {
		t.Fatal("unescaped prompt rendered as HTML")
	}
	deepPath := filepath.Join(dir, "v2", "deep", id+".json")
	deepBytes, err := os.ReadFile(deepPath)
	if err != nil {
		t.Fatal(err)
	}
	skill.Corpus[0].SourceSHA256 = report.SourceSHA256
	skill.Corpus[0].DeepReportSHA256 = fmt.Sprintf("%x", sha256.Sum256(deepBytes))
	if !s.skillCorpusFresh(skill) {
		t.Fatal("matching Deep bytes should be fresh")
	}
	report.Unknowns = []string{"later analytical correction"}
	writeJSONFixture(t, deepPath, report)
	if s.skillCorpusFresh(skill) {
		t.Fatal("changed Deep analysis with the same source hash must be stale")
	}
}

func TestSkillAnalysisRequestIsLocalAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := &server{dataDir: dir}
	request := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/skills/analyze", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.requestSkillAnalysis(w, r)
		return w
	}
	if got := request("https://other.example").Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin request status %d", got)
	}
	if got := request("http://127.0.0.1:8787").Code; got != http.StatusSeeOther {
		t.Fatalf("local request status %d", got)
	}
	first, err := s.skillAnalysisRequest()
	if err != nil || first.Status != "pending" {
		t.Fatalf("request: %+v, %v", first, err)
	}
	if got := request("http://127.0.0.1:8787").Code; got != http.StatusSeeOther {
		t.Fatalf("duplicate status %d", got)
	}
	second, _ := s.skillAnalysisRequest()
	if second.RequestedAt != first.RequestedAt {
		t.Fatal("duplicate request reset the queue entry")
	}
	if info, err := os.Stat(s.skillAnalysisRequestPath()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private request permissions: %v %v", info, err)
	}
}

func TestAnalysisRequestIsPrivateIdempotentAndSameOrigin(t *testing.T) {
	dir := t.TempDir()
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeJSONFixture(t, filepath.Join(dir, "deep", id+".json"), DeepReport{Kind: "deep", SessionID: id, Task: "task", Outcome: "unknown", OutcomeBasis: "basis"})
	s := &server{dataDir: dir}
	request := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/sessions/"+id+"/analyze", nil)
		r.SetPathValue("id", id)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.requestAnalysis(w, r)
		return w
	}
	if got := request("https://other.example").Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin status %d", got)
	}
	if got := request("http://127.0.0.1:8787").Code; got != http.StatusSeeOther {
		t.Fatalf("same-origin status %d", got)
	}
	if got := request("http://127.0.0.1:8787").Code; got != http.StatusSeeOther {
		t.Fatalf("duplicate status %d", got)
	}
	path := s.analysisRequestPath(id)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("request permissions %v", info.Mode().Perm())
	}
	requestFile, err := s.analysisRequest(id)
	if err != nil || requestFile.Status != "pending" || requestFile.SessionID != id {
		t.Fatalf("request: %+v, %v", requestFile, err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("duplicate requests: %v, %v", files, err)
	}
	requestFile.Status = "failed"
	requestFile.ErrorCode = "invalid_analysis"
	writeJSONFixture(t, path, requestFile)
	if got := request("http://127.0.0.1:8787").Code; got != http.StatusSeeOther {
		t.Fatalf("retry status %d", got)
	}
	retried, err := s.analysisRequest(id)
	if err != nil || retried.Status != "pending" || retried.RetryCount != 1 || retried.LastErrorCode != "invalid_analysis" || retried.ErrorCode != "" {
		t.Fatalf("failed request not retried safely: %+v, %v", retried, err)
	}
}

func TestDeepLinkCreatesReviewRequest(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, raw := range []string{id, "codex://threads/" + id, "https://chatgpt.com/c/" + id} {
		if got := sessionIDFromLink(raw); got != id {
			t.Fatalf("parse %q: %q", raw, got)
		}
	}
	for _, raw := range []string{"https://other.example/c/" + id, "codex://threads/no-id", "codex://threads/" + id + "/" + id} {
		if got := sessionIDFromLink(raw); got != "" {
			t.Fatalf("accepted invalid deep link %q", raw)
		}
	}
	dir := t.TempDir()
	writeJSONFixture(t, filepath.Join(dir, "deep", id+".json"), DeepReport{Kind: "deep", SessionID: id, Task: "task", Outcome: "unknown", OutcomeBasis: "basis"})
	s := &server{dataDir: dir}
	form := url.Values{"deep_link": {"codex://threads/" + id}}.Encode()
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/sessions/analyze-link", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://127.0.0.1:8787")
	w := httptest.NewRecorder()
	s.requestAnalysisLink(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("deep link request status %d: %s", w.Code, w.Body.String())
	}
	if request, err := s.analysisRequest(id); err != nil || request.Status != "pending" {
		t.Fatalf("deep link not queued: %+v, %v", request, err)
	}
}

func TestPublishedSessionQueuesOnlyFreshReviewRequestAndShowsAvailableButton(t *testing.T) {
	dir := t.TempDir()
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	source := filepath.Join(dir, "rollout-"+id+".jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeJSONFixture(t, filepath.Join(dir, "v2", "sources", id+".json"), map[string]any{"session_id": id, "source_path": source})
	writeJSONFixture(t, filepath.Join(dir, "v2", "deep", id+".json"), v2Fixture(id))
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer ch.Close()
	s := &server{dataDir: dir, clickhouse: ch.URL, client: ch.Client()}
	post := func() int {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/sessions/"+id+"/analyze", nil)
		r.SetPathValue("id", id)
		r.Header.Set("Origin", "http://127.0.0.1:8787")
		w := httptest.NewRecorder()
		s.requestAnalysis(w, r)
		return w.Code
	}
	if got := post(); got != http.StatusSeeOther {
		t.Fatalf("published rerun status %d", got)
	}
	request, err := s.analysisRequest(id)
	if err != nil || request.Status != "pending" || !request.RefreshSource {
		t.Fatalf("rerun not queued: %+v, %v", request, err)
	}
	request.Status, request.Published, request.SourceSHA256 = "completed", true, strings.Repeat("a", 64)
	writeJSONFixture(t, s.analysisRequestPath(id), request)
	if got := post(); got != http.StatusSeeOther {
		t.Fatalf("second rerun status %d", got)
	}
	repeated, err := s.analysisRequest(id)
	if err != nil || repeated.Status != "pending" || repeated.RerunCount != 1 || repeated.SourceSHA256 != "" || !repeated.RefreshSource {
		t.Fatalf("published rerun did not request fresh source: %+v, %v", repeated, err)
	}
	if got := post(); got != http.StatusSeeOther {
		t.Fatalf("pending idempotent status %d", got)
	}
	stillPending, _ := s.analysisRequest(id)
	if stillPending.RerunCount != 1 {
		t.Fatalf("pending request duplicated: %+v", stillPending)
	}
	page := func() string {
		r := httptest.NewRequest(http.MethodGet, "/sessions/"+id, nil)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.sessionPage(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("session page status %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if strings.Contains(page(), "Повторно разобрать обновлённую сессию") {
		t.Fatal("rerun button shown while request pending")
	}
	stillPending.Status, stillPending.Published = "completed", true
	writeJSONFixture(t, s.analysisRequestPath(id), stillPending)
	if !strings.Contains(page(), "Повторно разобрать обновлённую сессию") {
		t.Fatal("published session has no working rerun button")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page(), "Повторно разобрать обновлённую сессию") {
		t.Fatal("rerun button shown without local source")
	}
	if got := post(); got != http.StatusNotFound {
		t.Fatalf("missing source rerun status %d", got)
	}
}

func TestGroupedProposalHasUsableAnchor(t *testing.T) {
	proposal := V2Proposal{ProposalID: "p2:9cf0305dc9b116f94988", Action: "Проверить предложение", Scope: "local", Readiness: V2State{Status: "hypothesis"}}
	var output strings.Builder
	writeV2ProposalCard(&output, proposal, true)
	if !strings.Contains(output.String(), `/recommendations#proposal-p2-9cf0305dc9b116f94988`) {
		t.Fatalf("grouped proposal link is not usable: %s", output.String())
	}
}
