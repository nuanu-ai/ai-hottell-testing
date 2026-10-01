package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A request records an explicit local intent to analyze a selected session.
// Agent execution is a separate step and its state is reported by the worker.
type AnalysisRequest struct {
	SessionID       string `json:"session_id"`
	Status          string `json:"status"`
	RequestedAt     string `json:"requested_at"`
	RefreshSource   bool   `json:"refresh_source,omitempty"`
	RerunCount      int    `json:"rerun_count,omitempty"`
	RetryCount      int    `json:"retry_count,omitempty"`
	LastErrorCode   string `json:"last_error_code,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	Error           string `json:"error,omitempty"`
	SourceSHA256    string `json:"source_sha256,omitempty"`
	SourceRecords   int    `json:"source_records,omitempty"`
	ResultPath      string `json:"result_path,omitempty"`
	Published       bool   `json:"published,omitempty"`
	Validation      string `json:"validation,omitempty"`
	ProposalRefresh string `json:"proposal_refresh,omitempty"`
}

var analysisSessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var analysisLinkID = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
var analysisRequestMu sync.Mutex

func sessionIDFromLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if analysisSessionID.MatchString(strings.ToLower(raw)) {
		return strings.ToLower(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || !oneOf(u.Scheme, "codex", "https", "http") {
		return ""
	}
	if oneOf(u.Scheme, "https", "http") && !oneOf(strings.ToLower(u.Hostname()), "chatgpt.com", "www.chatgpt.com") {
		return ""
	}
	ids := analysisLinkID.FindAllString(raw, -1)
	if len(ids) != 1 {
		return ""
	}
	return strings.ToLower(ids[0])
}

func localSessionExists(id string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	root := filepath.Join(home, ".codex", "sessions")
	found := false
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), id+".jsonl") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func (s *server) analysisSourceAvailable(id string) bool {
	for _, path := range []string{
		filepath.Join(s.dataDir, "v2", "sources", id+".json"),
		filepath.Join(s.dataDir, "telemetry", "manifest.json"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.HasSuffix(path, "manifest.json") {
			var manifest struct {
				Sessions []struct {
					SessionID  string `json:"session_id"`
					SourcePath string `json:"source_path"`
				} `json:"sessions"`
			}
			if json.Unmarshal(data, &manifest) == nil {
				for _, item := range manifest.Sessions {
					if item.SessionID == id && item.SourcePath != "" {
						if info, err := os.Stat(item.SourcePath); err == nil && info.Mode().IsRegular() {
							return true
						}
					}
				}
			}
			continue
		}
		var descriptor struct {
			SessionID  string `json:"session_id"`
			SourcePath string `json:"source_path"`
		}
		if json.Unmarshal(data, &descriptor) == nil && descriptor.SessionID == id && descriptor.SourcePath != "" {
			if info, err := os.Stat(descriptor.SourcePath); err == nil && info.Mode().IsRegular() {
				return true
			}
		}
	}
	return localSessionExists(id)
}

func (s *server) requestAnalysisLink(w http.ResponseWriter, r *http.Request) {
	if !validLocalPost(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid deep link", http.StatusBadRequest)
		return
	}
	id := sessionIDFromLink(r.FormValue("deep_link"))
	if id == "" {
		http.Error(w, "deep link must contain exactly one local session ID", http.StatusBadRequest)
		return
	}
	r.SetPathValue("id", id)
	s.requestAnalysis(w, r)
}

func analysisRequestLabel(request AnalysisRequest) string {
	switch request.Status {
	case "pending":
		return "Ожидает запуска анализатора"
	case "running":
		return "Анализ идёт"
	case "completed":
		if request.Published {
			return "Анализ опубликован"
		}
		return "Кандидат готов, ожидает смысловой проверки и публикации"
	case "failed":
		return "Анализ завершился ошибкой: " + request.ErrorCode
	default:
		return "Статус неизвестен"
	}
}

func (s *server) analysisRequestPath(id string) string {
	return filepath.Join(s.dataDir, "v2", "requests", id+".json")
}

func (s *server) analysisRequest(id string) (AnalysisRequest, error) {
	if !analysisSessionID.MatchString(id) {
		return AnalysisRequest{}, os.ErrNotExist
	}
	f, err := os.Open(s.analysisRequestPath(id))
	if err != nil {
		return AnalysisRequest{}, err
	}
	defer f.Close()
	var request AnalysisRequest
	if err := json.NewDecoder(io.LimitReader(f, 32<<10)).Decode(&request); err != nil {
		return AnalysisRequest{}, err
	}
	if request.SessionID != id || !oneOf(request.Status, "pending", "running", "completed", "failed") {
		return AnalysisRequest{}, fmt.Errorf("invalid analysis request")
	}
	return request, nil
}

func (s *server) analysisRequestAPI(w http.ResponseWriter, r *http.Request) {
	request, err := s.analysisRequest(r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, request)
}

func validLocalPost(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}

func (s *server) requestAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !analysisSessionID.MatchString(id) {
		serveError(w, os.ErrNotExist)
		return
	}
	if !validLocalPost(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	published := s.v2DeepExists(id)
	if published && !s.analysisSourceAvailable(id) {
		http.Error(w, "local session source unavailable", http.StatusNotFound)
		return
	}
	if !s.telemetryExists(id) && !s.deepExists(id) && !localSessionExists(id) {
		if _, err := s.session(r.Context(), id); err != nil {
			serveError(w, err)
			return
		}
	}
	analysisRequestMu.Lock()
	defer analysisRequestMu.Unlock()
	prior, err := s.analysisRequest(id)
	if err == nil {
		if prior.Status == "failed" || (published && prior.Status == "completed" && prior.Published) {
			if prior.Status == "failed" {
				prior.LastErrorCode = prior.ErrorCode
				prior.RetryCount++
			} else {
				prior.RerunCount++
			}
			prior.RefreshSource = published
			prior.Status = "pending"
			prior.RequestedAt = time.Now().UTC().Format(time.RFC3339)
			prior.StartedAt, prior.CompletedAt, prior.ErrorCode, prior.Error = "", "", "", ""
			prior.ResultPath, prior.Validation, prior.ProposalRefresh = "", "", ""
			prior.Published = false
			if published {
				prior.SourceSHA256 = ""
				prior.SourceRecords = 0
			}
			if err := writeAnalysisRequestAtomic(s.analysisRequestPath(id), prior); err != nil {
				serveError(w, err)
				return
			}
		}
		http.Redirect(w, r, "/sessions/"+id, http.StatusSeeOther)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		serveError(w, err)
		return
	}
	path := s.analysisRequestPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		serveError(w, err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		http.Redirect(w, r, "/sessions/"+id, http.StatusSeeOther)
		return
	}
	if err != nil {
		serveError(w, err)
		return
	}
	request := AnalysisRequest{SessionID: id, Status: "pending", RequestedAt: time.Now().UTC().Format(time.RFC3339), RefreshSource: published}
	if err := json.NewEncoder(f).Encode(request); err != nil {
		_ = f.Close()
		serveError(w, err)
		return
	}
	if err := f.Close(); err != nil {
		serveError(w, err)
		return
	}
	http.Redirect(w, r, "/sessions/"+id, http.StatusSeeOther)
}

func writeAnalysisRequestAtomic(path string, request any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".analysis-request-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if err := json.NewEncoder(f).Encode(request); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
