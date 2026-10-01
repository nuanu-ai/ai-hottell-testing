package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SkillReport struct {
	Kind          string             `json:"kind"`
	SchemaVersion int                `json:"schema_version"`
	AnalyzedAt    string             `json:"analyzed_at"`
	Corpus        []SkillCorpusEntry `json:"corpus"`
	Inventory     struct {
		SnapshotAt string               `json:"snapshot_at"`
		Scope      string               `json:"scope"`
		Items      []SkillInventoryItem `json:"items"`
	} `json:"inventory"`
	Opportunities []SkillOpportunity `json:"opportunities"`
}

type SkillInventoryItem struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	DescriptionSHA256 string `json:"description_sha256"`
	PathClass         string `json:"path_class"`
}

type SkillCorpusEntry struct {
	SessionID        string `json:"session_id"`
	SourceSHA256     string `json:"source_sha256"`
	DeepReportSHA256 string `json:"deep_report_sha256"`
}

type SkillOpportunity struct {
	ID                     string       `json:"id"`
	Kind                   string       `json:"kind"`
	Title                  string       `json:"title"`
	Recommendation         string       `json:"recommendation"`
	WhySkill               string       `json:"why_skill"`
	Alternative            string       `json:"alternative"`
	Uncertainty            string       `json:"uncertainty"`
	Verification           string       `json:"verification"`
	Readiness              string       `json:"readiness"`
	Sources                []V2Source   `json:"sources"`
	InstalledSkill         string       `json:"installed_skill,omitempty"`
	HistoricalAvailability string       `json:"historical_availability,omitempty"`
	CopyPrompt             string       `json:"copy_prompt,omitempty"`
	ExternalSkill          *SkillRemote `json:"external_skill,omitempty"`
}

type SkillRemote struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	ReviewStatus string `json:"review_status"`
}

func (s *server) skillReport() (SkillReport, error) {
	path := filepath.Join(s.dataDir, "v2", "skill-opportunities.json")
	f, err := os.Open(path)
	if err != nil {
		return SkillReport{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 1<<20 {
		return SkillReport{}, fmt.Errorf("skill report exceeds size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	var report SkillReport
	if err := decoder.Decode(&report); err != nil {
		return SkillReport{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return SkillReport{}, fmt.Errorf("skill report has trailing content")
	}
	if report.Kind != "skill_opportunities" || report.SchemaVersion != 1 || report.AnalyzedAt == "" {
		return SkillReport{}, fmt.Errorf("invalid skill report identity")
	}
	for _, item := range report.Opportunities {
		if !safeID.MatchString(item.ID) || !oneOf(item.Kind, "use_existing", "create", "install_candidate") || item.Title == "" || item.Recommendation == "" || len(item.Sources) == 0 {
			return SkillReport{}, fmt.Errorf("invalid skill opportunity")
		}
		if item.Kind == "create" && item.CopyPrompt == "" || item.Kind == "use_existing" && item.HistoricalAvailability != "unknown" || item.Kind == "install_candidate" && (item.ExternalSkill == nil || !strings.HasPrefix(item.ExternalSkill.URL, "https://")) {
			return SkillReport{}, fmt.Errorf("invalid skill opportunity target")
		}
	}
	return report, nil
}

func (s *server) skillReportFresh(report SkillReport) bool {
	home, err := os.UserHomeDir()
	if err != nil || !skillInventoryCurrent(report.Inventory.SnapshotAt, report.Inventory.Items, home) {
		return false
	}
	return s.skillCorpusFresh(report)
}

func (s *server) skillCorpusFresh(report SkillReport) bool {
	files, err := os.ReadDir(filepath.Join(s.dataDir, "v2", "deep"))
	if err != nil {
		return false
	}
	if len(files) != len(report.Corpus) {
		return false
	}
	for _, item := range report.Corpus {
		deep, err := s.v2Deep(item.SessionID)
		if err != nil || deep.SourceSHA256 != item.SourceSHA256 {
			return false
		}
		content, err := os.ReadFile(filepath.Join(s.dataDir, "v2", "deep", item.SessionID+".json"))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(content)) != item.DeepReportSHA256 {
			return false
		}
	}
	return true
}

func skillInventoryCurrent(snapshotAt string, items []SkillInventoryItem, home string) bool {
	at, err := time.Parse(time.RFC3339Nano, snapshotAt)
	if err != nil {
		return false
	}
	want := make(map[string]bool, len(items))
	for _, item := range items {
		want[item.PathClass] = true
	}
	seen := make(map[string]bool, len(items))
	for _, root := range []string{".codex/skills", ".agents/skills"} {
		entries, err := os.ReadDir(filepath.Join(home, root))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == ".system" {
				continue
			}
			path := filepath.Join(home, root, entry.Name(), "SKILL.md")
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() {
				return false
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return false
			}
			front, ok := strings.CutPrefix(string(content), "---\n")
			if !ok {
				continue // Plain Markdown is not in the frontmatter-based inventory.
			}
			front, _, ok = strings.Cut(front, "---\n")
			if !ok || !strings.Contains("\n"+front, "\nname:") || !strings.Contains("\n"+front, "\ndescription:") {
				continue
			}
			if info.ModTime().After(at) {
				return false
			}
			label := "~/" + filepath.ToSlash(filepath.Join(root, entry.Name()))
			if !want[label] || seen[label] {
				return false
			}
			seen[label] = true
		}
	}
	return len(seen) == len(want)
}

func skillKindName(kind string) string {
	switch kind {
	case "use_existing":
		return "Использовать установленный"
	case "create":
		return "Создать свой"
	default:
		return "Проверить готовый извне"
	}
}

func skillReadinessName(value string) string {
	switch value {
	case "prepared":
		return "подготовлено к рассмотрению"
	case "candidate":
		return "кандидат для пробы"
	default:
		return "гипотеза"
	}
}

func (s *server) writeSkillOpportunities(b *strings.Builder) {
	b.WriteString(`<section id="skills"><div class="tile-head"><h2>Возможности для skills</h2><span class="tag purple">анализ между сессиями</span></div>`)
	b.WriteString(`<form method="post" action="/skills/analyze"><button type="submit" class="copy-skill-prompt">Обновить анализ skills</button></form>`)
	if request, err := s.skillAnalysisRequest(); err == nil {
		switch request.Status {
		case "pending":
			b.WriteString(`<p class="helper">Запрос ожидает локального анализатора.</p>`)
		case "running":
			b.WriteString(`<p class="helper">Анализ skills идёт.</p>`)
		case "completed":
			b.WriteString(`<p class="helper">Новый кандидат готов и ожидает смысловой проверки перед публикацией.</p>`)
		case "published":
			b.WriteString(`<p class="helper">Обновлённый анализ skills проверен и опубликован в локальном дашборде.</p>`)
		case "failed":
			fmt.Fprintf(b, `<p class="unknown">Анализ skills не завершился: %s.</p>`, esc(request.ErrorCode))
		}
	}
	report, err := s.skillReport()
	if errors.Is(err, os.ErrNotExist) {
		b.WriteString(`<p class="unknown">Общий анализ skills ещё не подготовлен.</p></section>`)
		return
	}
	if err != nil {
		b.WriteString(`<p class="unknown">Отчёт skills не прошёл чтение. Проверьте приватный файл отчёта.</p></section>`)
		return
	}
	fresh := s.skillReportFresh(report)
	fmt.Fprintf(b, `<p class="helper">Срез: %d сессий · пользовательские skills проверены %s. Системные и plugin skills в этот список не входят. Историческую доступность и использование skill нельзя вывести из нынешнего списка.</p>`, len(report.Corpus), esc(report.Inventory.SnapshotAt))
	if !fresh {
		b.WriteString(`<p class="unknown">После этого анализа изменились Deep-отчёты или локальные skills. Рекомендации ниже требуют обновления.</p>`)
	}
	b.WriteString(`<div class="proposal-grid">`)
	for _, item := range report.Opportunities {
		fmt.Fprintf(b, `<article class="tile proposal-card" id="skill-%s"><div class="tags"><span class="tag purple">%s</span><span class="tag outline">%s</span></div><h3>%s</h3><p>%s</p><p class="helper">Почему skill: %s</p><p class="helper">Альтернатива: %s</p><p class="helper">Что неизвестно: %s</p><p class="helper">Как проверить пользу: %s</p>`, esc(item.ID), esc(skillKindName(item.Kind)), esc(skillReadinessName(item.Readiness)), esc(item.Title), esc(item.Recommendation), esc(item.WhySkill), esc(item.Alternative), esc(item.Uncertainty), esc(item.Verification))
		if item.Kind == "use_existing" {
			fmt.Fprintf(b, `<p class="helper">Установлен сейчас: <strong>%s</strong>. Доступность в момент старых сессий неизвестна.</p>`, esc(item.InstalledSkill))
		}
		if item.ExternalSkill != nil {
			fmt.Fprintf(b, `<p class="helper">Источник для проверки: <a href="%s" rel="noopener noreferrer" target="_blank">%s</a>. Установка требует проверки файлов, лицензии и версии.</p>`, esc(item.ExternalSkill.URL), esc(item.ExternalSkill.Name))
		}
		if item.CopyPrompt != "" {
			fmt.Fprintf(b, `<label for="prompt-%s">Промпт для создания skill</label><textarea id="prompt-%s" class="skill-prompt" readonly>%s</textarea><button type="button" class="copy-skill-prompt" data-copy-target="prompt-%s">Скопировать промпт</button>`, esc(item.ID), esc(item.ID), esc(item.CopyPrompt), esc(item.ID))
		}
		b.WriteString(`<details class="proposal-evidence"><summary>Основания по сессиям</summary><ul>`)
		for _, source := range item.Sources {
			if !safeID.MatchString(source.SessionID) {
				continue
			}
			fmt.Fprintf(b, `<li><a href="/sessions/%s/deep">%s</a> · %s · %s</li>`, esc(source.SessionID), esc(shortID(source.SessionID)), esc(source.TaskID), esc(joinedEvidence(source.Evidence)))
		}
		b.WriteString(`</ul></details></article>`)
	}
	b.WriteString(`</div></section><script src="/skills.js" defer></script>`)
}

func (s *server) skillReportAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.skillReport()
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"report": report, "fresh": s.skillReportFresh(report)})
}
