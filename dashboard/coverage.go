package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Detector names and order follow analytics/catalogue.yaml v0.1 on the
// docs/analysis-concept-2026-09-29 branch. Every session must account for all 13.
var detectorCatalogue = []struct{ ID, Name string }{
	{"D01", "Постановка задачи"},
	{"D03", "Повтор без прогресса"},
	{"D05", "«Готово» без подтверждения"},
	{"D07", "Каскад ошибок среды"},
	{"D10", "Повторяющаяся инструкция"},
	{"D12", "Агент забыл сказанное"},
	{"D13", "Выброс затрат"},
	{"D16", "Простой на человеке"},
	{"D19", "Пропущенный skill"},
	{"D22", "Не тот класс работы"},
	{"D24", "Неработающий MCP"},
	{"D25", "Skill без результата"},
	{"D26", "Устаревшие настройки"},
}

type DetectorCheck struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	Evidence    []string   `json:"evidence"`
	MissingData []string   `json:"missing_data"`
	PeerSources []V2Source `json:"peer_sources,omitempty"`
}

type CoverageReport struct {
	Kind             string          `json:"kind"`
	SchemaVersion    int             `json:"schema_version"`
	CatalogueVersion string          `json:"catalogue_version"`
	SessionID        string          `json:"session_id"`
	SourceSHA256     string          `json:"source_sha256"`
	Checks           []DetectorCheck `json:"checks"`
}

func (s *server) coverage(id string) (CoverageReport, error) {
	if !safeID.MatchString(id) {
		return CoverageReport{}, os.ErrNotExist
	}
	if s.v2DeepExists(id) {
		report, err := s.v2Deep(id)
		if err != nil {
			return CoverageReport{}, err
		}
		return v2Coverage(report), nil
	}
	return s.legacyCoverage(id)
}

func (s *server) legacyCoverage(id string) (CoverageReport, error) {
	if !safeID.MatchString(id) {
		return CoverageReport{}, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.dataDir, "coverage", id+".json"))
	if err != nil {
		return CoverageReport{}, err
	}
	defer f.Close()
	var report CoverageReport
	decoder := json.NewDecoder(io.LimitReader(f, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return CoverageReport{}, err
	}
	_, hashErr := hex.DecodeString(report.SourceSHA256)
	if report.Kind != "detector_coverage" || report.SchemaVersion != 1 || report.CatalogueVersion != "0.1" || report.SessionID != id || len(report.SourceSHA256) != 64 || hashErr != nil {
		return CoverageReport{}, fmt.Errorf("invalid detector coverage identity")
	}
	if len(report.Checks) != len(detectorCatalogue) {
		return CoverageReport{}, fmt.Errorf("detector coverage must include all 13 checks")
	}
	for i, check := range report.Checks {
		if check.ID != detectorCatalogue[i].ID || check.Summary == "" {
			return CoverageReport{}, fmt.Errorf("invalid detector check at position %d", i)
		}
		switch check.Status {
		case "suspected", "checked_clear":
			if len(check.Evidence) == 0 {
				return CoverageReport{}, fmt.Errorf("detector check %s lacks evidence", check.ID)
			}
		case "insufficient_data":
			if len(check.MissingData) == 0 {
				return CoverageReport{}, fmt.Errorf("detector check %s lacks missing-data reason", check.ID)
			}
		case "not_checked":
			// The source exists, but analysis has not yet covered it reliably.
		case "not_applicable":
		default:
			return CoverageReport{}, fmt.Errorf("invalid detector check status %s", check.Status)
		}
	}
	return report, nil
}

func (s *server) coverageAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.coverage(r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, report)
}

func checkLabel(status string) string {
	switch status {
	case "suspected":
		return "Есть сигнал · ждёт проверки"
	case "checked_clear":
		return "Проверено · не найдено"
	case "insufficient_data":
		return "Недостаточно данных"
	case "not_checked":
		return "Не проверено · нужен разбор"
	case "not_applicable":
		return "Не относится к сессии"
	default:
		return "Не проверено"
	}
}

func checkColor(status string) string {
	switch status {
	case "suspected":
		return "orange"
	case "checked_clear":
		return "green"
	case "insufficient_data":
		return "yellow"
	default:
		return "outline"
	}
}

func writeCoverageDetail(b *strings.Builder, report CoverageReport) {
	b.WriteString(`<article class="tile"><div class="tile-head"><h2>Покрытие всех 13 проверок</h2><span class="tag purple">Deep · каталог v0.1</span></div><p class="helper">Сигнал — кандидат на подтверждение, а «проверено, не найдено» означает, что нужный участок данных действительно просмотрен. Эти статусы не подменяют решение человека.</p><div class="coverage-list">`)
	for i, check := range report.Checks {
		fmt.Fprintf(b, `<details class="coverage-check"><summary><span class="mono">%s</span><span>%s</span><span class="tag %s">%s</span></summary><p>%s</p>`, esc(check.ID), esc(detectorCatalogue[i].Name), checkColor(check.Status), esc(checkLabel(check.Status)), esc(check.Summary))
		if len(check.Evidence) > 0 {
			fmt.Fprintf(b, `<p class="helper">Основания: %s</p>`, esc(strings.Join(check.Evidence, ", ")))
		}
		if len(check.PeerSources) > 0 {
			b.WriteString(`<p class="helper">Связанные задания в других сессиях:</p><ul class="evidence-list">`)
			for _, source := range check.PeerSources {
				fmt.Fprintf(b, `<li><a href="/sessions/%s/deep">%s</a> · %s · %s</li>`, esc(source.SessionID), esc(shortID(source.SessionID)), esc(source.TaskID), esc(strings.Join(source.Evidence, ", ")))
			}
			b.WriteString(`</ul>`)
		}
		if len(check.MissingData) > 0 {
			fmt.Fprintf(b, `<p class="helper">Не хватает: %s</p>`, esc(strings.Join(check.MissingData, "; ")))
		}
		b.WriteString(`</details>`)
	}
	fmt.Fprintf(b, `</div><p class="foot"><a href="/api/reports/coverage/%s">JSON покрытия</a></p></article>`, esc(report.SessionID))
}

func (s *server) coveragePage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.sessions(r.Context())
	if err != nil {
		serveError(w, err)
		return
	}
	counts := make(map[string]map[string]int, len(detectorCatalogue))
	byDetector := make(map[string][]struct{ ID, Status string }, len(detectorCatalogue))
	for _, detector := range detectorCatalogue {
		counts[detector.ID] = make(map[string]int)
	}
	selected := 0
	for _, row := range rows {
		if !row.HasDeep && !row.HasTelemetry {
			continue
		}
		selected++
		report, err := s.coverage(row.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			serveError(w, err)
			return
		}
		for _, detector := range detectorCatalogue {
			status := "not_checked"
			if err == nil {
				for _, check := range report.Checks {
					if check.ID == detector.ID {
						status = check.Status
						break
					}
				}
			}
			counts[detector.ID][status]++
			byDetector[detector.ID] = append(byDetector[detector.ID], struct{ ID, Status string }{row.ID, status})
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p class="summary">Все 13 пунктов каталога v0.1 для %d сессий с отчётами. «Не проверено» означает незавершённый разбор доступных записей; «недостаточно данных» означает отсутствующий вход. «Проверено, не найдено» показано отдельно.</p><p class="helper">Это углублённый разбор сессий. ID только с Hook-событиями без полного транскрипта здесь не оцениваются. Технические выводы из Hooks и OTel остаются отдельными.</p><div class="coverage-grid">`, selected)
	for _, detector := range detectorCatalogue {
		c := counts[detector.ID]
		fmt.Fprintf(&b, `<article class="tile coverage-card"><div class="tile-head"><h2><span class="mono">%s</span> · %s</h2></div><div class="tags"><span class="tag orange">сигнал %d</span><span class="tag green">проверено %d</span><span class="tag yellow">нет данных %d</span><span class="tag outline">не относится %d</span><span class="tag outline">не проверено %d</span></div><details><summary>Сессии и статусы</summary><div class="coverage-sessions">`, esc(detector.ID), esc(detector.Name), c["suspected"], c["checked_clear"], c["insufficient_data"], c["not_applicable"], c["not_checked"])
		for _, item := range byDetector[detector.ID] {
			fmt.Fprintf(&b, `<div><a class="mono" href="/sessions/%s/deep#coverage">%s</a><span class="tag %s">%s</span></div>`, esc(item.ID), esc(shortID(item.ID)), checkColor(item.Status), esc(checkLabel(item.Status)))
		}
		b.WriteString(`</div></details></article>`)
	}
	b.WriteString(`</div>`)
	render(w, "Покрытие концепта", "coverage", template.HTML(b.String()))
}
