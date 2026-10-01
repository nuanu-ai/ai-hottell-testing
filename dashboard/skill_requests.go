package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type SkillAnalysisRequest struct {
	Status      string `json:"status"`
	RequestedAt string `json:"requested_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Candidate   string `json:"candidate,omitempty"`
}

func (s *server) skillAnalysisRequestPath() string {
	return filepath.Join(s.dataDir, "v2", "skill-analysis-request.json")
}

func (s *server) skillAnalysisRequest() (SkillAnalysisRequest, error) {
	var request SkillAnalysisRequest
	data, err := os.ReadFile(s.skillAnalysisRequestPath())
	if err != nil {
		return request, err
	}
	if len(data) > 4096 || json.Unmarshal(data, &request) != nil || !oneOf(request.Status, "pending", "running", "completed", "published", "failed") {
		return request, errors.New("invalid skill analysis request")
	}
	return request, nil
}

func (s *server) requestSkillAnalysis(w http.ResponseWriter, r *http.Request) {
	if !validLocalPost(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	analysisRequestMu.Lock()
	defer analysisRequestMu.Unlock()
	previous, err := s.skillAnalysisRequest()
	if err == nil && oneOf(previous.Status, "pending", "running") {
		http.Redirect(w, r, "/recommendations#skills", http.StatusSeeOther)
		return
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		serveError(w, err)
		return
	}
	request := SkillAnalysisRequest{Status: "pending", RequestedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := writeAnalysisRequestAtomic(s.skillAnalysisRequestPath(), request); err != nil {
		serveError(w, err)
		return
	}
	http.Redirect(w, r, "/recommendations#skills", http.StatusSeeOther)
}

func (s *server) skillAnalysisRequestAPI(w http.ResponseWriter, r *http.Request) {
	request, err := s.skillAnalysisRequest()
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, request)
}
