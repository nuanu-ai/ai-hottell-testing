package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// This service reads the local ClickHouse stand. It never writes session data
// to ClickHouse or sends it to another host.
type server struct {
	clickhouse      string
	dataDir         string
	collectorConfig string
	client          *http.Client
}

type Session struct {
	ID           string `json:"session_id"`
	Agent        string `json:"agent"`
	First        string `json:"first_event"`
	Last         string `json:"last_event"`
	Events       uint64 `json:"hook_events"`
	Prompts      uint64 `json:"prompt_hooks"`
	ToolStarts   uint64 `json:"tool_start_hooks"`
	ToolEnds     uint64 `json:"tool_end_hooks"`
	HasStart     uint8  `json:"has_session_start"`
	HasEnd       uint8  `json:"has_session_end"`
	HasDeep      bool   `json:"has_deep_report"`
	HasHooks     bool   `json:"has_hook_report"`
	HasTelemetry bool   `json:"has_telemetry_report"`
}

type HookEvent struct {
	At     string `json:"at"`
	Name   string `json:"name"`
	Tool   string `json:"tool,omitempty"`
	CallID string `json:"call_id,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
}

type HookReport struct {
	Kind        string      `json:"kind"`
	Source      string      `json:"source"`
	Session     Session     `json:"session"`
	Events      []HookEvent `json:"events"`
	Coverage    []string    `json:"coverage"`
	Unavailable []string    `json:"unavailable"`
}

// DeepReport is authored by an analytical agent after reading the complete
// selected session. Evidence IDs must point back to session steps.
type DeepReport struct {
	Kind            string           `json:"kind"`
	SessionID       string           `json:"session_id"`
	Task            string           `json:"task"`
	Outcome         string           `json:"outcome"`
	OutcomeBasis    string           `json:"outcome_basis"`
	ModelOpinion    string           `json:"model_opinion,omitempty"`
	Observations    []Observation    `json:"observations"`
	Recommendations []Recommendation `json:"recommendations"`
	Unknowns        []string         `json:"unknowns"`
}

type Observation struct {
	Pattern  string   `json:"pattern"`
	Finding  string   `json:"finding"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

type Recommendation struct {
	Action   string   `json:"action"`
	Target   string   `json:"target"`
	Change   string   `json:"change"`
	Evidence []string `json:"evidence"`
	Status   string   `json:"status"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

//go:embed design.css
var designCSS string

//go:embed skills.js
var skillsJS string

func main() {
	listen := flag.String("listen", "127.0.0.1:8787", "local dashboard address")
	clickhouse := flag.String("clickhouse", "http://127.0.0.1:8123", "local ClickHouse HTTP endpoint")
	dataDir := flag.String("data-dir", "local-data/reports", "private deep report directory")
	collectorConfig := flag.String("collector-config", "~/.config/hottell/config.json", "local hottell collector config")
	flag.Parse()
	addr, err := net.ResolveTCPAddr("tcp", *listen)
	if err != nil || addr == nil || !addr.IP.IsLoopback() {
		log.Fatal("dashboard must listen on a loopback address")
	}
	u, err := url.Parse(*clickhouse)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" {
		log.Fatal("clickhouse must be a local HTTP URL")
	}
	ip := net.ParseIP(u.Hostname())
	if (ip == nil || !ip.IsLoopback()) && u.Hostname() != "localhost" {
		log.Fatal("clickhouse must be local")
	}
	s := &server{clickhouse: strings.TrimRight(*clickhouse, "/"), dataDir: *dataDir, collectorConfig: *collectorConfig, client: &http.Client{Timeout: 25 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /design.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(w, designCSS)
	})
	mux.HandleFunc("GET /skills.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(w, skillsJS)
	})
	mux.HandleFunc("GET /", s.overview)
	mux.HandleFunc("GET /sessions", s.sessionsPage)
	mux.HandleFunc("GET /sessions/{id}", s.sessionPage)
	mux.HandleFunc("GET /sessions/{id}/telemetry", s.telemetryPage)
	mux.HandleFunc("GET /sessions/{id}/deep", s.deepPage)
	mux.HandleFunc("GET /coverage", s.coveragePage)
	mux.HandleFunc("GET /recommendations", s.recommendationsPage)
	mux.HandleFunc("GET /api/sessions", s.sessionsAPI)
	mux.HandleFunc("GET /api/reports/hooks/{id}", s.hooksAPI)
	mux.HandleFunc("GET /api/reports/otel/{id}", s.otelAPI)
	mux.HandleFunc("GET /api/reports/delivery/{id}", s.deliveryAPI)
	mux.HandleFunc("GET /api/reports/deep/{id}", s.deepAPI)
	mux.HandleFunc("GET /api/reports/telemetry/{id}", s.telemetryAPI)
	mux.HandleFunc("GET /api/reports/coverage/{id}", s.coverageAPI)
	mux.HandleFunc("GET /api/reports/deep-v2/{id}", s.v2DeepAPI)
	mux.HandleFunc("GET /api/proposals", s.v2ProposalsAPI)
	mux.HandleFunc("GET /api/skill-opportunities", s.skillReportAPI)
	mux.HandleFunc("POST /skills/analyze", s.requestSkillAnalysis)
	mux.HandleFunc("GET /api/skill-analysis-request", s.skillAnalysisRequestAPI)
	mux.HandleFunc("POST /sessions/{id}/analyze", s.requestAnalysis)
	mux.HandleFunc("POST /sessions/analyze-link", s.requestAnalysisLink)
	mux.HandleFunc("GET /api/analysis-requests/{id}", s.analysisRequestAPI)
	log.Printf("AI Hottell dashboard: http://%s", *listen)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func (s *server) query(ctx context.Context, sql string, params url.Values, out any) error {
	u, err := url.Parse(s.clickhouse)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("readonly", "1")
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(sql+"\nFORMAT JSON"))
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("ClickHouse %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var result struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	return json.Unmarshal(result.Data, out)
}

const sessionsSQL = `SELECT
  LogAttributes['session_id'] AS session_id,
  any(LogAttributes['agent']) AS agent,
  toString(min(Timestamp)) AS first_event,
  toString(max(Timestamp)) AS last_event,
  count() AS hook_events,
  countIf(Body = 'agent.hook.UserPromptSubmit') AS prompt_hooks,
  countIf(Body = 'agent.hook.PreToolUse') AS tool_start_hooks,
  countIf(Body IN ('agent.hook.PostToolUse', 'agent.hook.PostToolUseFailure')) AS tool_end_hooks,
  toUInt8(countIf(Body = 'agent.hook.SessionStart') > 0) AS has_session_start,
  toUInt8(countIf(Body = 'agent.hook.SessionEnd') > 0) AS has_session_end
FROM otel.otel_logs
WHERE ServiceName = 'agent-hooks' AND LogAttributes['session_id'] != ''
GROUP BY session_id ORDER BY last_event DESC`

func (s *server) sessions(ctx context.Context) ([]Session, error) {
	var rows []Session
	err := s.query(ctx, sessionsSQL, nil, &rows)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(rows))
	for i := range rows {
		rows[i].HasHooks = true
		rows[i].HasDeep = s.deepExists(rows[i].ID)
		rows[i].HasTelemetry = s.telemetryExists(rows[i].ID)
		seen[rows[i].ID] = true
	}
	files, err := os.ReadDir(filepath.Join(s.dataDir, "deep"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range files {
		id := strings.TrimSuffix(file.Name(), ".json")
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") || !safeID.MatchString(id) || seen[id] {
			continue
		}
		row := Session{ID: id, Agent: "codex", HasDeep: true, HasTelemetry: s.telemetryExists(id)}
		if row.HasTelemetry {
			if telemetry, err := s.telemetry(id); err == nil {
				row.Last = telemetry.Period["last_recorded_at"]
			}
		}
		rows = append(rows, row)
		seen[id] = true
	}
	files, err = os.ReadDir(filepath.Join(s.dataDir, "v2", "deep"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range files {
		id := strings.TrimSuffix(file.Name(), ".json")
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") || !safeID.MatchString(id) || seen[id] {
			continue
		}
		row := Session{ID: id, Agent: "codex", HasDeep: true, HasTelemetry: s.telemetryExists(id)}
		if row.HasTelemetry {
			if telemetry, err := s.telemetry(id); err == nil {
				row.Last = telemetry.Period["last_recorded_at"]
			}
		}
		rows = append(rows, row)
		seen[id] = true
	}
	files, err = os.ReadDir(filepath.Join(s.dataDir, "telemetry"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range files {
		id := strings.TrimSuffix(file.Name(), ".json")
		if file.IsDir() || id == "manifest" || !strings.HasSuffix(file.Name(), ".json") || !safeID.MatchString(id) || seen[id] {
			continue
		}
		row := Session{ID: id, Agent: "codex", HasTelemetry: true}
		if telemetry, err := s.telemetry(id); err == nil {
			row.Last = telemetry.Period["last_recorded_at"]
		}
		rows = append(rows, row)
		seen[id] = true
	}
	files, err = os.ReadDir(filepath.Join(s.dataDir, "v2", "requests"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range files {
		id := strings.TrimSuffix(file.Name(), ".json")
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") || !analysisSessionID.MatchString(id) || seen[id] {
			continue
		}
		request, err := s.analysisRequest(id)
		if err != nil {
			continue
		}
		rows = append(rows, Session{ID: id, Agent: "codex", Last: request.RequestedAt})
		seen[id] = true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Last != rows[j].Last {
			return rows[i].Last > rows[j].Last
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

func (s *server) session(ctx context.Context, id string) (Session, error) {
	rows, err := s.sessions(ctx)
	if err != nil {
		return Session{}, err
	}
	for _, row := range rows {
		if row.ID == id && row.HasHooks {
			return row, nil
		}
	}
	return Session{}, os.ErrNotExist
}

const eventsSQL = `SELECT
  toString(Timestamp) AS at,
  Body AS name,
  LogAttributes['tool_name'] AS tool,
  LogAttributes['tool_use_id'] AS call_id,
  LogAttributes['turn_id'] AS turn_id
FROM otel.otel_logs
WHERE ServiceName = 'agent-hooks' AND LogAttributes['session_id'] = {sid:String}
ORDER BY Timestamp`

func (s *server) hooks(ctx context.Context, id string) (HookReport, error) {
	if !safeID.MatchString(id) {
		return HookReport{}, os.ErrNotExist
	}
	session, err := s.session(ctx, id)
	if err != nil {
		return HookReport{}, err
	}
	params := url.Values{"param_sid": {id}}
	var events []HookEvent
	err = s.query(ctx, eventsSQL, params, &events)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return HookReport{}, err
	}
	coverage := []string{"Только события agent-hooks из локального ClickHouse; тексты промптов и содержимое инструментов не включены в отчёт."}
	if session.HasStart == 0 || session.HasEnd == 0 {
		coverage = append(coverage, "Границы сессии неполные; длительность сессии не вычисляется.")
	}
	return HookReport{Kind: "hooks", Source: "hottell/agent-hooks", Session: session, Events: events, Coverage: coverage,
		Unavailable: []string{"Исход задачи и качество: нет подтверждения", "Токены и стоимость: хуки не предоставляют", "Повторная работа человека: нет разметки", "Причины ошибок и рекомендации: требуют глубокого разбора"}}, nil
}

func (s *server) deepPath(id string) string { return filepath.Join(s.dataDir, "deep", id+".json") }
func (s *server) deepExists(id string) bool {
	if !safeID.MatchString(id) {
		return false
	}
	if s.v2DeepExists(id) {
		return true
	}
	_, err := os.Stat(s.deepPath(id))
	return err == nil
}

func (s *server) deep(id string) (DeepReport, error) {
	if !safeID.MatchString(id) {
		return DeepReport{}, os.ErrNotExist
	}
	f, err := os.Open(s.deepPath(id))
	if err != nil {
		return DeepReport{}, err
	}
	defer f.Close()
	var report DeepReport
	dec := json.NewDecoder(io.LimitReader(f, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		return DeepReport{}, err
	}
	if report.Kind != "deep" || report.SessionID != id {
		return DeepReport{}, fmt.Errorf("invalid deep report identity")
	}
	if report.Outcome != "unknown" && report.Outcome != "verified" && report.Outcome != "partial" && report.Outcome != "failed" {
		return DeepReport{}, fmt.Errorf("invalid outcome")
	}
	if report.Task == "" || report.OutcomeBasis == "" {
		return DeepReport{}, fmt.Errorf("task and outcome basis are required")
	}
	for _, item := range report.Observations {
		if item.Finding == "" || len(item.Evidence) == 0 {
			return DeepReport{}, fmt.Errorf("observation without evidence")
		}
	}
	for _, item := range report.Recommendations {
		if item.Action == "" || item.Change == "" || len(item.Evidence) == 0 || item.Status != "draft" {
			return DeepReport{}, fmt.Errorf("recommendation must be an evidenced draft")
		}
	}
	return report, nil
}

func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func serveError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Print(err)
	http.Error(w, "local data unavailable", http.StatusServiceUnavailable)
}

func (s *server) sessionsAPI(w http.ResponseWriter, r *http.Request) {
	rows, err := s.sessions(r.Context())
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, rows)
}
func (s *server) hooksAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.hooks(r.Context(), r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, report)
}
func (s *server) deepAPI(w http.ResponseWriter, r *http.Request) {
	if s.v2DeepExists(r.PathValue("id")) {
		s.v2DeepAPI(w, r)
		return
	}
	report, err := s.deep(r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, report)
}

const page = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>AI Hottell — {{.Title}}</title><link rel="stylesheet" href="/design.css"></head>
<body><header class="shell"><div class="shell-row"><div class="brand"><b>AI Hottell <span>2.0</span></b><span>Анализ сессий</span></div><div class="shell-right"><span class="period">Локальный стенд</span><span class="demo-tag">Записанные и восстановленные данные отмечены отдельно</span></div></div></header>
<div class="tabs-wrap"><nav class="tabs" aria-label="Экраны">
<a class="tab" href="/" aria-selected="{{eq .Selected "overview"}}">Обзор</a>
<a class="tab" href="/sessions" aria-selected="{{eq .Selected "sessions"}}">Сессии</a>
<a class="tab" href="/coverage" aria-selected="{{eq .Selected "coverage"}}">13 проверок</a>
<a class="tab" href="/recommendations" aria-selected="{{eq .Selected "recommendations"}}">Решения и выводы</a>
</nav></div><main><section class="page"><div class="page-head"><h1>{{.Title}}</h1></div>{{.Body}}</section></main></body></html>`

type safeHTML = template.HTML

func render(w http.ResponseWriter, title, selected string, body safeHTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t := template.Must(template.New("page").Parse(page))
	_ = t.Execute(w, map[string]any{"Title": title, "Selected": selected, "Body": body})
}
func esc(v string) string { return template.HTMLEscapeString(v) }

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.sessions(r.Context())
	if err != nil {
		serveError(w, err)
		return
	}
	deep := 0
	hooks := 0
	telemetry := 0
	selected := 0
	outcomes := map[string]int{}
	events := uint64(0)
	for _, row := range rows {
		events += row.Events
		if row.HasHooks {
			hooks++
		}
		if row.HasDeep {
			deep++
			if s.v2DeepExists(row.ID) {
				if report, err := s.v2Deep(row.ID); err == nil {
					for _, task := range report.Tasks {
						outcomes[task.Outcome]++
					}
				}
			} else if report, err := s.deep(row.ID); err == nil {
				outcomes[report.Outcome]++
			}
		}
		if row.HasTelemetry {
			telemetry++
		}
		if row.HasTelemetry || row.HasDeep {
			selected++
		}
	}
	var priority strings.Builder
	if err := s.writePriorityDecisions(&priority, 3); err != nil {
		serveError(w, err)
		return
	}
	body := priority.String() + fmt.Sprintf(`<p class="summary"><b>%d сессий в наборе отчётов</b> · %d с ретроспективной хронологией · %d с Deep · %d новых ID и заявок</p><div class="grid"><article class="tile span-8"><div class="tile-head"><h2>Сэкономлено вашего времени</h2><span class="label">после подтверждённых изменений</span></div><div class="saved-number">нет данных</div><p class="helper">Для оценки нужны внедрённое изменение и сравнимые задачи после него. Время агента и качество результата пока неизвестны.</p></article><article class="tile span-4"><div class="tile-head"><h2>Входящие</h2></div><div class="list"><a class="row-btn" href="/sessions#incoming"><span class="dot flag"></span><span>Новые и неразобранные</span><span class="n">%d</span><span>→</span></a><a class="row-btn" href="/recommendations"><span class="dot"></span><span>Два вида выводов</span><span class="n">→</span></a></div></article><article class="tile span-12"><div class="tile-head"><h2>Источники данных</h2><span class="label">%d ID с Hooks · %d событий</span></div><p class="helper">Историческая реконструкция, записанные Hooks и связанные OTel показаны отдельно. События без ID сессии нельзя приписать отдельному отчёту.</p></article></div>`, selected, telemetry, deep, len(rows)-selected, len(rows)-selected, hooks, events)
	if deep > 0 {
		body += fmt.Sprintf(`<article class="tile"><div class="tile-head"><h2>Исходы по глубоким отчётам</h2><span class="label">в 2.0 — по заданиям; в старом формате — по сессиям</span></div><div class="outcome-grid"><div><strong>%d</strong><span>подтверждён</span></div><div><strong>%d</strong><span>частичный</span></div><div><strong>%d</strong><span>не достигнут</span></div><div><strong>%d</strong><span>неизвестен</span></div></div></article>`, outcomes["verified"], outcomes["partial"], outcomes["failed"], outcomes["unknown"])
	}
	if len(rows) == 0 {
		body += `<p class="helper">Сессий пока нет. Проверьте сбор Hottell и проведите сессию.</p>`
	}
	render(w, "Обзор", "overview", safeHTML(body))
}

func (s *server) sessionsPage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.sessions(r.Context())
	if err != nil {
		serveError(w, err)
		return
	}
	var b strings.Builder
	b.WriteString(`<article class="tile"><h2>Разобрать новую сессию</h2><p class="helper">Укажите локальную ссылку Codex или ID сессии. Заявка появится здесь; Deep публикуется только после отдельной смысловой проверки.</p><form method="post" action="/sessions/analyze-link"><label for="deep-link">Ссылка на сессию</label><input id="deep-link" name="deep_link" required placeholder="codex://threads/…" style="display:block;width:min(100%,650px);margin:8px 0 12px;padding:10px"><button class="btn" type="submit">Разобрать новую сессию</button></form></article>`)
	var selected, incoming []Session
	for _, row := range rows {
		if row.HasTelemetry || row.HasDeep {
			selected = append(selected, row)
		} else {
			incoming = append(incoming, row)
		}
	}
	fmt.Fprintf(&b, `<p class="summary">%d сессий с подготовленным отчётом. Техническая страница разделяет историческую реконструкцию и текущие записанные Hooks/OTel.</p><h2>Набор отчётов</h2>`, len(selected))
	writeSessionsTable(&b, selected)
	fmt.Fprintf(&b, `<h2 id="incoming">Новые и ещё не разобранные сессии · %d</h2><p class="helper">Здесь видны ID из потока Hooks и заявки по локальным ссылкам. Hook-события и сама заявка ещё не означают готовый Deep-отчёт.</p>`, len(incoming))
	writeSessionsTable(&b, incoming)
	render(w, "Сессии", "sessions", safeHTML(b.String()))
}

func writeSessionsTable(b *strings.Builder, rows []Session) {
	if len(rows) == 0 {
		b.WriteString(`<p class="empty">Здесь пока нет сессий.</p>`)
		return
	}
	b.WriteString(`<article class="tile"><div class="table-wrap"><table><thead><tr><th>Последнее событие</th><th>Агент</th><th>Сессия</th><th class="num">Записанные Hooks</th><th>Отчёты</th></tr></thead><tbody>`)
	for _, row := range rows {
		fmt.Fprintf(b, `<tr><td>%s</td><td><span class="tag blue">%s</span></td><td><a href="/sessions/%s" class="mono">%s</a></td><td class="num">%d</td><td><div class="tags">`, esc(row.Last), esc(row.Agent), esc(row.ID), esc(row.ID), row.Events)
		if row.HasHooks || row.HasTelemetry {
			fmt.Fprintf(b, `<a class="tag blue" href="/sessions/%s/telemetry">Технический</a>`, esc(row.ID))
		}
		if row.HasDeep {
			fmt.Fprintf(b, `<a class="tag purple" href="/sessions/%s/deep">Deep</a>`, esc(row.ID))
		}
		b.WriteString(`</div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div></article>`)
}

func (s *server) sessionPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := s.sessions(r.Context())
	if err != nil {
		serveError(w, err)
		return
	}
	var current Session
	for _, row := range rows {
		if row.ID == id {
			current = row
			break
		}
	}
	if current.ID == "" {
		serveError(w, os.ErrNotExist)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p><a href="/sessions">← Все сессии</a></p><p class="mono">%s · %s</p><div class="grid">`, esc(id), esc(current.Agent))
	fmt.Fprintf(&b, `<article class="tile span-5"><div class="tile-head"><h2>Технический отчёт</h2><span class="tag blue">Hooks + OTel</span></div><p class="helper">Записанные события и восстановленная хронология с раздельной маркировкой источников.</p><p>%s</p><a class="btn" href="/sessions/%s/telemetry">Открыть отчёт →</a></article>`, technicalStatus(current), esc(id))
	fmt.Fprintf(&b, `<article class="tile span-7"><div class="tile-head"><h2>Глубокий отчёт</h2><span class="tag purple">анализ агентом</span></div><p class="helper">Задача, исход, подтверждённые наблюдения, неизвестное и черновики изменений.</p><p>%s</p>`, deepStatus(current))
	if current.HasDeep {
		fmt.Fprintf(&b, `<a class="btn" href="/sessions/%s/deep">Открыть отчёт →</a>`, esc(id))
	}
	b.WriteString(`</article></div>`)
	if s.v2DeepExists(id) {
		b.WriteString(`<article class="tile"><div class="tags"><span class="tag green">Deep 2.0 готов</span></div><p class="helper">В отчёте выделены задания и показаны все 13 проверок.</p>`)
		request, requestErr := s.analysisRequest(id)
		if requestErr != nil && !errors.Is(requestErr, os.ErrNotExist) {
			serveError(w, requestErr)
			return
		}
		if requestErr == nil && (!request.Published || request.Status == "pending" || request.Status == "running") {
			fmt.Fprintf(&b, `<p>%s</p><a href="/api/analysis-requests/%s">Статус заявки в JSON</a>`, esc(analysisRequestLabel(request)), esc(id))
		}
		if s.analysisSourceAvailable(id) && (errors.Is(requestErr, os.ErrNotExist) || request.Status == "failed" || (request.Status == "completed" && request.Published)) {
			fmt.Fprintf(&b, `<form method="post" action="/sessions/%s/analyze"><button class="btn" type="submit">Повторно разобрать обновлённую сессию</button></form><p class="helper">Новая версия источника создаст кандидата. Публикация потребует отдельной смысловой проверки; при той же версии повтор не создаётся.</p>`, esc(id))
		}
		b.WriteString(`</article>`)
	} else if request, err := s.analysisRequest(id); err == nil {
		fmt.Fprintf(&b, `<article class="tile"><h2>Разбор 2.0</h2><p>%s</p><p class="helper">Заявка создана %s. Статус не означает, что Deep опубликован или проверен по смыслу.</p><a href="/api/analysis-requests/%s">Статус заявки в JSON</a>`, esc(analysisRequestLabel(request)), esc(request.RequestedAt), esc(id))
		if request.Status == "failed" {
			fmt.Fprintf(&b, `<form method="post" action="/sessions/%s/analyze"><button class="btn" type="submit">Повторить анализ</button></form>`, esc(id))
		}
		b.WriteString(`</article>`)
	} else if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(&b, `<article class="tile"><h2>Разобрать сессию по формату 2.0</h2><p class="helper">Заявка ставит сессию в очередь разбора. Отчёт 2.0 появится после отдельной проверки результата.</p><form method="post" action="/sessions/%s/analyze"><button class="btn" type="submit">Разобрать новую сессию</button></form></article>`, esc(id))
	} else {
		serveError(w, err)
		return
	}
	render(w, "Сессия", "sessions", safeHTML(b.String()))
}

func technicalStatus(row Session) string {
	if row.HasHooks {
		return "Есть записанные Hooks"
	}
	if row.HasTelemetry {
		return "Есть восстановленная хронология · Hooks/OTel могут отсутствовать"
	}
	return "Технических данных пока нет"
}
func deepStatus(row Session) string {
	if row.HasDeep {
		return "Глубокий разбор готов"
	}
	if row.HasTelemetry {
		return "Глубокий разбор ещё готовится"
	}
	return "Есть только события сборщика; для Deep нужны полная сессия и проверяемый исход"
}

func (s *server) recommendationsPage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.sessions(r.Context())
	if err != nil {
		serveError(w, err)
		return
	}
	if registry, err := s.v2Registry(); err == nil {
		s.recommendationsPageV2(w, r, registry, rows)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		serveError(w, err)
		return
	}
	var b strings.Builder
	b.WriteString(`<p class="summary">Два независимых вида выводов: технический по записанным Hooks/OTel и Deep по содержанию сессии. Историческая реконструкция отмечена отдельно.</p><nav class="report-index"><a href="#technical">Hooks и OTel</a><a href="#deep">Deep</a><a href="#detectors">13 проверок</a><a href="#proposals">Предложения</a></nav><article class="tile" id="technical"><div class="tile-head"><h2>Выводы из Hooks и OTel</h2><span class="tag blue">технические данные</span></div><p class="helper">Только полнота записи и связанные по ID события. Они не подтверждают исход или качество работы агента.</p><div class="finding-list">`)
	type technicalGroup struct {
		finding TechnicalFinding
		ids     []string
	}
	groups := make(map[string]*technicalGroup)
	var groupOrder []string
	for _, row := range rows {
		var retro *TelemetryReport
		if row.HasTelemetry {
			if report, err := s.telemetry(row.ID); err == nil {
				retro = &report
			}
		}
		otel, otelErr := s.otel(r.Context(), row.ID)
		var recordedOTel *OTelReport
		if otelErr == nil {
			recordedOTel = &otel
		}
		var recordedHook *Session
		if row.HasHooks {
			recordedHook = &row
		}
		for _, finding := range technicalFindings(retro, recordedHook, row.HasHooks, recordedOTel, otelErr == nil) {
			group := groups[finding.Code]
			if group == nil {
				group = &technicalGroup{finding: finding}
				groups[finding.Code] = group
				groupOrder = append(groupOrder, finding.Code)
			}
			group.ids = append(group.ids, row.ID)
		}
	}
	for _, code := range groupOrder {
		group := groups[code]
		fmt.Fprintf(&b, `<div class="finding"><div class="tags"><span class="tag blue">Hooks / OTel</span><span class="tag outline">%d сессий</span></div><h3>%s</h3><p>%s</p><p class="helper">Доказательства в технических отчётах: `, len(group.ids), esc(group.finding.Finding), esc(group.finding.Meaning))
		for i, id := range group.ids {
			if i > 0 {
				b.WriteString(` · `)
			}
			fmt.Fprintf(&b, `<a href="/sessions/%s/telemetry">%s</a>`, esc(id), esc(shortID(id)))
		}
		b.WriteString(`</p></div>`)
	}
	if len(groupOrder) == 0 {
		b.WriteString(`<p class="unknown">Проверяемых технических выводов пока нет.</p>`)
	}
	b.WriteString(`</div></article><article class="tile" id="deep"><div class="tile-head"><h2>Выводы Deep</h2><span class="tag purple">анализ сессий</span></div><p class="helper">Наблюдения аналитических агентов с доказательствами и статусом. Откройте сессию, чтобы увидеть её выводы.</p><div class="finding-list">`)
	deepCount := 0
	var proposals []struct {
		id string
		p  Recommendation
	}
	for _, row := range rows {
		deep, err := s.deep(row.ID)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, `<details class="report-session"><summary><span class="mono">%s</span><span>%d выводов · %d предложений</span></summary><div class="finding-list">`, esc(shortID(row.ID)), len(deep.Observations), len(deep.Recommendations))
		for _, o := range deep.Observations {
			deepCount++
			fmt.Fprintf(&b, `<div class="finding"><div class="tags"><span class="tag purple">Deep</span><span class="tag %s">%s</span><a class="tag outline" href="/sessions/%s/deep">сессия %s</a></div><h3>%s</h3><p class="helper">Доказательства: %s</p></div>`, statusColor(o.Status), esc(o.Status), esc(row.ID), esc(shortID(row.ID)), esc(o.Finding), esc(strings.Join(o.Evidence, ", ")))
		}
		b.WriteString(`</div></details>`)
		for _, p := range deep.Recommendations {
			proposals = append(proposals, struct {
				id string
				p  Recommendation
			}{row.ID, p})
		}
	}
	if deepCount == 0 {
		b.WriteString(`<p class="unknown">Выводов Deep пока нет.</p>`)
	}
	b.WriteString(`</div></article><article class="tile" id="detectors"><div class="tile-head"><h2>Повторный Deep-анализ · 13 проверок</h2><span class="tag purple">кандидаты</span></div><p class="helper">Здесь показаны сигналы повторного разбора. Статусы без сигнала, с нехваткой данных и неприменимые случаи видны в <a href="/coverage">полной матрице</a> и отчёте каждой сессии.</p><div class="finding-list">`)
	detectorSignals := 0
	for _, row := range rows {
		if !row.HasDeep {
			continue
		}
		coverage, err := s.coverage(row.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			serveError(w, err)
			return
		}
		for i, check := range coverage.Checks {
			if check.Status != "suspected" {
				continue
			}
			detectorSignals++
			fmt.Fprintf(&b, `<div class="finding"><div class="tags"><span class="tag purple">%s · %s</span><span class="tag orange">ждёт проверки</span><a class="tag outline" href="/sessions/%s/deep#coverage">сессия %s</a></div><h3>%s</h3><p class="helper">Основания: %s</p></div>`, esc(check.ID), esc(detectorCatalogue[i].Name), esc(row.ID), esc(shortID(row.ID)), esc(check.Summary), esc(strings.Join(check.Evidence, ", ")))
		}
	}
	if detectorSignals == 0 {
		b.WriteString(`<p class="unknown">Сигналов пока нет. Откройте матрицу, чтобы отличить проверенные пункты от пунктов без данных.</p>`)
	}
	b.WriteString(`</div></article><h2 id="proposals">Предложения из Deep</h2><p class="helper">Для применения каждого предложения нужно отдельное решение и проверка результата после изменения.</p><div class="recs">`)
	for _, item := range proposals {
		p := item.p
		fmt.Fprintf(&b, `<article class="tile rec"><span class="tag blue">черновик</span><h2 class="rec-title">%s</h2><p class="helper">%s</p><pre class="change">%s</pre><p class="helper">Доказательства: %s · <a href="/sessions/%s/deep">сессия</a></p></article>`, esc(p.Action), esc(p.Target), esc(p.Change), esc(strings.Join(p.Evidence, ", ")), esc(item.id))
	}
	if len(proposals) == 0 {
		b.WriteString(`<article class="tile span-12"><p class="empty">Черновиков пока нет. Они появятся после глубокого разбора сессий.</p></article>`)
	}
	b.WriteString(`</div>`)
	render(w, "Выводы и предложения", "recommendations", safeHTML(b.String()))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
