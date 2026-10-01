package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func readinessName(status string) string {
	switch status {
	case "hypothesis":
		return "Проверить гипотезу"
	case "needs_specification":
		return "Подготовить изменение"
	case "prepared_verified":
		return "Рассмотреть применение"
	default:
		return "Готовность неизвестна"
	}
}

func decisionName(status string) string {
	switch status {
	case "accepted":
		return "Принято"
	case "rejected":
		return "Отклонено"
	case "revision_requested":
		return "На доработке"
	default:
		return "Решение не запрошено"
	}
}

func executionName(status string) string {
	if status == "applied" {
		return "Применено"
	}
	return "Не применено"
}

func effectName(status string) string {
	switch status {
	case "helped":
		return "Помогло"
	case "no_effect":
		return "Без эффекта"
	case "worse":
		return "Стало хуже"
	case "insufficient_data":
		return "Мало данных"
	default:
		return "Не измерен"
	}
}

func writeV2ProposalCard(b *strings.Builder, p V2Proposal, compact bool) {
	anchorID := strings.ReplaceAll(p.ProposalID, ":", "-")
	anchor := "proposal-" + anchorID
	if !safeID.MatchString(anchorID) {
		anchor = "proposal-unknown"
	}
	if compact {
		fmt.Fprintf(b, `<article class="tile decision-card"><div class="tags"><span class="tag blue">%s</span><span class="tag outline">%s</span></div><h3>%s</h3><p>%s</p><p class="helper">Область: %s · %d %s</p><p class="helper">Следующий шаг: %s</p><a href="/recommendations#%s">Основания и проверка →</a></article>`, esc(p.Priority), esc(readinessName(p.Readiness.Status)), esc(p.Action), esc(p.Observed), esc(p.Scope), len(p.Sources), sessionWord(len(p.Sources)), esc(readinessName(p.Readiness.Status)), esc(anchor))
		return
	}
	fmt.Fprintf(b, `<article class="tile proposal-card" id="%s"><div class="tags"><span class="tag blue">%s</span><span class="tag purple">%s</span><span class="tag outline">%s</span></div><h2>%s</h2><p class="helper">%s · %s</p><dl class="proposal-facts"><div><dt>Что произошло</dt><dd>%s</dd></div><div><dt>Почему</dt><dd>%s</dd></div><div><dt>Что уже настроено</dt><dd>%s</dd></div><div><dt>Что изменить и где</dt><dd>%s · %s <span class="mono">%s</span></dd></div><div><dt>Как проверить</dt><dd>%s</dd></div></dl>`, esc(anchor), esc(p.Priority), esc(p.ChangeType), esc(readinessName(p.Readiness.Status)), esc(p.Action), esc(p.ProposalID), esc(p.Scope), fallback(p.Observed), fallback(p.Cause), esc(existingRuleSummary(p.ExistingRule)), fallback(p.Change), fallback(p.Target.Kind), fallback(p.Target.Locator), fallback(p.Verification))
	if p.SelectionReason != "" {
		fmt.Fprintf(b, `<p class="helper">Почему выбран этот тип изменения: %s</p>`, esc(p.SelectionReason))
	}
	if p.Target.Version != "" {
		fmt.Fprintf(b, `<p class="helper">Текущая версия объекта: <span class="mono">%s</span></p>`, esc(p.Target.Version))
	}
	if p.ExpectedEffect != "" {
		fmt.Fprintf(b, `<p class="helper">Ожидаемый эффект: %s</p>`, esc(p.ExpectedEffect))
	}
	if p.Readiness.Reason != "" {
		fmt.Fprintf(b, `<p class="helper">Готовность: %s</p>`, esc(p.Readiness.Reason))
	}
	fmt.Fprintf(b, `<div class="state-row"><span>Решение: %s</span><span>Исполнение: %s</span><span>Эффект: %s</span></div>`, esc(decisionName(p.Decision.Status)), esc(executionName(p.Execution.Status)), esc(effectName(p.Effect.Status)))
	if p.Effect.Note != "" {
		fmt.Fprintf(b, `<p class="helper">Проверка эффекта: %s</p>`, esc(p.Effect.Note))
	}
	if p.Effect.Metric != "" {
		fmt.Fprintf(b, `<p class="helper">Метрика: %s · до %d, после %d наблюдений · %s</p>`, esc(p.Effect.Metric), p.Effect.NBefore, p.Effect.NAfter, esc(p.Effect.Method))
	}
	b.WriteString(`<details class="proposal-evidence"><summary>Основания, условия и откат</summary><div class="proposal-evidence-body">`)
	if len(p.Sources) > 0 {
		b.WriteString(`<ul>`)
		for _, source := range p.Sources {
			fmt.Fprintf(b, `<li><a href="/sessions/%s/deep">%s</a> · задание %s · %s</li>`, esc(source.SessionID), esc(shortID(source.SessionID)), esc(source.TaskID), esc(joinedEvidence(source.Evidence)))
		}
		b.WriteString(`</ul>`)
	}
	writeStringList(b, "Предусловия", p.Preconditions)
	writeStringList(b, "Исключения", p.Exceptions)
	writeStringList(b, "Альтернативные причины", p.AlternativeCauses)
	if p.HistoryReview != "" {
		fmt.Fprintf(b, `<p><strong>Проверка истории:</strong> %s</p>`, esc(p.HistoryReview))
	}
	if p.AutomationBasis != nil {
		basis := p.AutomationBasis
		writeStringList(b, "Шаги автоматизации", basis.Steps)
		fmt.Fprintf(b, `<p><strong>Частота и польза:</strong> %s · %s</p><p><strong>Существующие запуски:</strong> %s</p><p><strong>Исключения, права, остановка:</strong> %s · %s · %s</p>`, esc(basis.Frequency), esc(basis.Benefit), esc(basis.ExistingAutomationsChecked), esc(basis.ExceptionHandling), esc(basis.PermissionScope), esc(basis.StopMethod))
	}
	if p.Rollback != "" {
		fmt.Fprintf(b, `<p><strong>Откат:</strong> %s</p>`, esc(p.Rollback))
	}
	b.WriteString(`</div></details></article>`)
}

func fallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Пока не установлено"
	}
	return esc(value)
}

func existingRuleSummary(rule V2ExistingRule) string {
	switch rule.Status {
	case "found":
		parts := []string{"Найдено"}
		for _, s := range []string{rule.Description, rule.Locator, rule.Version} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " · ")
	case "not_found":
		return "Поиск выполнен: существующее правило не найдено"
	default:
		return "Наличие правила ещё не проверено"
	}
}

func sessionWord(n int) string {
	if n == 1 {
		return "источник"
	}
	return "источника"
}

func writeStringList(b *strings.Builder, title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, `<p><strong>%s:</strong> %s</p>`, esc(title), esc(strings.Join(values, "; ")))
}

func (s *server) writePriorityDecisions(b *strings.Builder, limit int) error {
	registry, err := s.v2Registry()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	items := sortedProposals(registry)
	var active []V2Proposal
	for _, p := range items {
		if p.Decision.Status != "rejected" && p.Execution.Status != "applied" {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if len(active) > limit {
		active = active[:limit]
	}
	fmt.Fprintf(b, `<section class="tile"><div class="tile-head"><h2>Приоритетные решения</h2><a href="/recommendations#proposals">Все предложения →</a></div><p class="helper">До трёх предложений с проверяемыми основаниями. Готовность, решение человека, применение и эффект независимы.</p><div class="decision-grid">`)
	for _, p := range active {
		writeV2ProposalCard(b, p, true)
	}
	b.WriteString(`</div></section>`)
	return nil
}

func (s *server) recommendationsPageV2(w http.ResponseWriter, r *http.Request, registry V2ProposalRegistry, rows []Session) {
	var b strings.Builder
	b.WriteString(`<p class="summary">AI Hottell 2.0 · решения на основе Deep-разбора. Отдельно показаны технические выводы из реально записанных Hooks/OTel и восстановленная хронология старых сессий.</p><nav class="report-index"><a href="#proposals">Предложения</a><a href="#skills">Возможности skills</a><a href="#technical">Технический отчёт</a><a href="#deep">Deep</a><a href="/coverage">13 проверок</a></nav><section id="proposals"><div class="tile-head"><h2>Предложения изменений</h2><span class="tag blue">общий реестр · дубли объединены</span></div><p class="helper">Готовность не означает принятие; принятие не означает применение. Перед применением нужна проверка актуальности объекта и отдельное решение владельца.</p><div class="proposal-grid">`)
	items := sortedProposals(registry)
	for _, p := range items {
		writeV2ProposalCard(&b, p, false)
	}
	if len(items) == 0 {
		b.WriteString(`<article class="tile"><p class="unknown">Подготовленных предложений пока нет. Откройте Deep-разборы сессий.</p></article>`)
	}
	b.WriteString(`</div></section>`)
	s.writeSkillOpportunities(&b)
	b.WriteString(`<section class="tile" id="technical"><div class="tile-head"><h2>Выводы из технических данных</h2><span class="tag blue">Hooks · OTel · реконструкция отдельно</span></div><p class="helper">Записанные Hooks и OTel не подтверждают исход задачи. Историческая хронология построена по транскрипту и помечена как реконструкция.</p>`)
	s.writeV2TechnicalFindings(&b, r.Context(), rows)
	b.WriteString(`<div class="report-links">`)
	for _, row := range rows {
		if row.HasTelemetry || row.HasHooks {
			fmt.Fprintf(&b, `<a href="/sessions/%s/telemetry">%s · технический отчёт</a>`, esc(row.ID), esc(shortID(row.ID)))
		}
	}
	b.WriteString(`</div></section><section class="tile" id="deep"><div class="tile-head"><h2>Deep по сессиям</h2><span class="tag purple">задания · исходы · 13 проверок</span></div><div class="finding-list">`)
	for _, row := range rows {
		if !row.HasDeep {
			continue
		}
		if s.v2DeepExists(row.ID) {
			report, err := s.v2Deep(row.ID)
			if err != nil {
				serveError(w, err)
				return
			}
			fmt.Fprintf(&b, `<div class="finding"><div class="tags"><span class="tag purple">Deep 2.0</span><a class="tag outline" href="/sessions/%s/deep">%s</a></div><h3>%d заданий · %d наблюдений</h3>`, esc(row.ID), esc(shortID(row.ID)), len(report.Tasks), len(report.Observations))
			for _, task := range report.Tasks {
				fmt.Fprintf(&b, `<p>%s · <strong>%s</strong> · %s</p>`, esc(task.TaskID), esc(task.Goal), esc(outcomeName(task.Outcome)))
			}
			b.WriteString(`</div>`)
		} else {
			fmt.Fprintf(&b, `<div class="finding"><a href="/sessions/%s/deep">%s · старый формат Deep</a></div>`, esc(row.ID), esc(shortID(row.ID)))
		}
	}
	b.WriteString(`</div></section>`)
	render(w, "Решения и выводы", "recommendations", template.HTML(b.String()))
}

func (s *server) writeV2TechnicalFindings(b *strings.Builder, ctx context.Context, rows []Session) {
	type group struct {
		finding TechnicalFinding
		ids     []string
	}
	groups := map[string]*group{}
	var order []string
	for _, row := range rows {
		var retro *TelemetryReport
		if row.HasTelemetry {
			if report, err := s.telemetry(row.ID); err == nil {
				retro = &report
			}
		}
		otel, err := s.otel(ctx, row.ID)
		var recordedOTel *OTelReport
		if err == nil {
			recordedOTel = &otel
		}
		var hook *Session
		if row.HasHooks {
			hook = &row
		}
		for _, finding := range technicalFindings(retro, hook, row.HasHooks, recordedOTel, err == nil) {
			g := groups[finding.Code]
			if g == nil {
				g = &group{finding: finding}
				groups[finding.Code] = g
				order = append(order, finding.Code)
			}
			g.ids = append(g.ids, row.ID)
		}
	}
	if len(order) == 0 {
		b.WriteString(`<p class="unknown">Проверяемых технических выводов пока нет.</p>`)
		return
	}
	b.WriteString(`<div class="finding-list">`)
	for _, code := range order {
		g := groups[code]
		fmt.Fprintf(b, `<div class="finding"><div class="tags"><span class="tag blue">%s</span><span class="tag outline">%d сессий</span></div><h3>%s</h3><p>%s</p><p class="helper">Основания: `, esc(code), len(g.ids), esc(g.finding.Finding), esc(g.finding.Meaning))
		for i, id := range g.ids {
			if i > 0 {
				b.WriteString(` · `)
			}
			fmt.Fprintf(b, `<a href="/sessions/%s/telemetry">%s</a>`, esc(id), esc(shortID(id)))
		}
		b.WriteString(`</p></div>`)
	}
	b.WriteString(`</div>`)
}

func (s *server) deepPageV2(w http.ResponseWriter, r *http.Request, report V2DeepReport) {
	var b strings.Builder
	id := report.SessionID
	fmt.Fprintf(&b, `<p><a href="/sessions/%s">← Сессия</a></p><p class="summary">Глубокий отчёт 2.0 · <span class="mono">%s</span> · %d заданий · источник SHA-256 <span class="mono">%s</span></p>`, esc(id), esc(id), len(report.Tasks), esc(report.SourceSHA256[:12]))
	b.WriteString(`<article class="tile"><div class="tile-head"><h2>Задания внутри сессии</h2><span class="tag purple">Deep · не телеметрия</span></div><p class="helper">Границы и исход каждого задания проверяются отдельно. Позднее исправление не стирает раннее необоснованное заявление «готово».</p><div class="task-grid">`)
	for _, task := range report.Tasks {
		fmt.Fprintf(&b, `<section class="task-card"><div class="tags"><span class="tag purple">%s</span><span class="tag %s">%s</span><span class="tag outline">L%d–L%d</span></div><h3>%s</h3><p class="helper">Область: %s</p>`, esc(task.TaskID), outcomeColor(task.Outcome), esc(outcomeName(task.Outcome)), task.StartLine, task.EndLine, esc(task.Goal), fallback(task.Scope))
		if task.ContinuedFrom != nil {
			previous := task.ContinuedFrom
			fmt.Fprintf(&b, `<p class="helper">Продолжение задания <a href="/sessions/%s/deep">%s · %s</a> · источник %s · основания: %s / %s</p>`,
				esc(url.PathEscape(previous.SessionID)), esc(previous.SessionID), esc(previous.TaskID), esc(previous.SourceSHA256[:12]),
				esc(joinedEvidence(previous.PreviousEvidence)), esc(joinedEvidence(previous.CurrentEvidence)))
		}
		writeStringList(&b, "Критерий результата", task.SuccessCriteria)
		writeStringList(&b, "Основание цели", task.GoalEvidence)
		writeStringList(&b, "Граница", task.BoundaryEvidence)
		writeStringList(&b, "Основание исхода", task.OutcomeBasis)
		if len(task.Claims) > 0 {
			b.WriteString(`<details><summary>Заявления о готовности</summary><ul>`)
			for _, claim := range task.Claims {
				fmt.Fprintf(&b, `<li><span class="mono">L%d</span> · %s · на момент заявления: %s · %s`, claim.Line, esc(claim.Claim), esc(claim.VerificationAtClaim), esc(joinedEvidence(claim.Evidence)))
				if claim.SubsequentResolution != "" {
					fmt.Fprintf(&b, ` · затем: %s`, esc(claim.SubsequentResolution))
				}
				b.WriteString(`</li>`)
			}
			b.WriteString(`</ul></details>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</div></article><article class="tile"><h2>Выводы Deep</h2><div class="finding-list">`)
	for _, o := range report.Observations {
		fmt.Fprintf(&b, `<div class="finding"><div class="tags"><span class="tag purple">%s</span><span class="tag %s">%s</span><span class="tag outline">%s</span></div><p>%s</p><p class="helper">Основания: %s</p></div>`, esc(o.Pattern), statusColor(o.Status), esc(o.Status), esc(strings.Join(o.TaskIDs, ", ")), esc(o.Finding), esc(joinedEvidence(o.Evidence)))
	}
	if len(report.Observations) == 0 {
		b.WriteString(`<p class="unknown">Наблюдений пока нет.</p>`)
	}
	b.WriteString(`</div></article><div id="coverage"></div>`)
	writeCoverageDetail(&b, v2Coverage(report))
	if previous, previousErr := s.legacyCoverage(id); previousErr == nil {
		writeCoverageChanges(&b, previous, v2Coverage(report))
	}
	registry, err := s.v2Registry()
	if err == nil {
		b.WriteString(`<section class="tile"><h2>Предложения, связанные с сессией</h2><div class="proposal-grid">`)
		count := 0
		for _, p := range sortedProposals(registry) {
			for _, source := range p.Sources {
				if source.SessionID == id {
					writeV2ProposalCard(&b, p, false)
					count++
					break
				}
			}
		}
		if count == 0 {
			b.WriteString(`<p class="unknown">Подготовленных предложений для этой сессии пока нет.</p>`)
		}
		b.WriteString(`</div></section>`)
	} else if errors.Is(err, os.ErrNotExist) {
		b.WriteString(`<section class="tile"><h2>Кандидаты этой сессии</h2><p class="helper">Общий реестр ещё не собран; эти предложения не объединены с другими сессиями.</p><div class="proposal-grid">`)
		for _, candidate := range report.ProposalCandidates {
			writeV2ProposalCard(&b, candidate, false)
		}
		if len(report.ProposalCandidates) == 0 {
			b.WriteString(`<p class="unknown">Кандидатов пока нет.</p>`)
		}
		b.WriteString(`</div></section>`)
	} else {
		serveError(w, err)
		return
	}
	if len(report.Unknowns) > 0 {
		b.WriteString(`<article class="tile"><h2>Остаётся неизвестным</h2><ul>`)
		for _, item := range report.Unknowns {
			fmt.Fprintf(&b, `<li>%s</li>`, esc(item))
		}
		b.WriteString(`</ul></article>`)
	}
	fmt.Fprintf(&b, `<p class="foot"><a href="/api/reports/deep/%s">JSON Deep 2.0</a></p>`, esc(id))
	render(w, "Глубокий отчёт 2.0", "sessions", template.HTML(b.String()))
}

func writeCoverageChanges(b *strings.Builder, previous, current CoverageReport) {
	changes := make([]string, 0)
	for i, check := range current.Checks {
		if i < len(previous.Checks) && previous.Checks[i].ID == check.ID && previous.Checks[i].Status != check.Status {
			changes = append(changes, fmt.Sprintf(`<li><span class="mono">%s</span> · было: %s → стало: %s</li>`, esc(check.ID), esc(checkLabel(previous.Checks[i].Status)), esc(checkLabel(check.Status))))
		}
	}
	if len(changes) == 0 {
		return
	}
	b.WriteString(`<article class="tile"><h2>Что изменилось после повторного разбора</h2><p class="helper">Сравнение статусов старого покрытия и Deep 2.0; причины и строки источника приведены выше в новых проверках. Старый отчёт сохранён локально.</p><ul>`)
	for _, item := range changes {
		b.WriteString(item)
	}
	b.WriteString(`</ul></article>`)
}

func v2Coverage(report V2DeepReport) CoverageReport {
	coverage := CoverageReport{Kind: "detector_coverage", SchemaVersion: 2, CatalogueVersion: "0.1", SessionID: report.SessionID, SourceSHA256: report.SourceSHA256}
	for _, c := range report.Checks {
		coverage.Checks = append(coverage.Checks, DetectorCheck{ID: c.ID, Status: c.Status, Summary: c.Summary, Evidence: c.Evidence, MissingData: c.MissingData, PeerSources: c.PeerSources})
	}
	return coverage
}
