package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

func (s *server) telemetryPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	report, retroErr := s.telemetry(id)
	if retroErr != nil && !errors.Is(retroErr, os.ErrNotExist) {
		serveError(w, retroErr)
		return
	}
	hook, hookErr := s.hooks(r.Context(), id)
	otel, otelErr := s.otel(r.Context(), id)
	delivery, deliveryErr := s.delivery(id)
	if deliveryErr != nil {
		serveError(w, deliveryErr)
		return
	}
	if report.SessionID == "" && hookErr != nil && (otelErr != nil || !otel.hasData()) && !delivery.hasEvidence() {
		serveError(w, hookErr)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p><a href="/sessions/%s">← Сессия</a></p><p class="summary">Технический отчёт · <span class="mono">%s</span></p>`, esc(id), esc(id))
	var retro *TelemetryReport
	if report.SessionID != "" {
		retro = &report
	}
	var recordedHook *Session
	if hookErr == nil {
		recordedHook = &hook.Session
	}
	var recordedOTel *OTelReport
	if otelErr == nil {
		recordedOTel = &otel
	}
	writeTechnicalFindings(&b, technicalFindings(retro, recordedHook, hookErr == nil, recordedOTel, otelErr == nil))
	writeDeliveryReport(&b, delivery)
	if report.SessionID != "" {
		b.WriteString(`<div class="grid"><article class="tile span-12"><div class="tile-head"><h2>Охват зафиксированного журнала</h2><span class="tag orange">восстановленный срез</span></div><p class="helper">Этот срез описывает данные из локального журнала сессии. Реально записанные Hooks и OTel показаны ниже отдельно.</p><div class="source-grid">`)
		for _, key := range []string{"hook", "otel", "transcript", "app"} {
			coverage := report.SourceCoverage[key]
			fmt.Fprintf(&b, `<div class="source-card"><div class="tags"><strong>%s</strong><span class="tag %s">%s</span></div><p class="helper">%s</p><details><summary class="helper">Подробности источника</summary><p class="helper">%s</p></details></div>`, esc(sourceName(key)), statusColor(coverage.Status), esc(statusName(coverage.Status)), esc(sourceSummary(key, coverage.Status)), esc(coverage.Detail))
		}
		b.WriteString(`</div></article>`)
		writeTelemetryMetrics(&b, report)
		b.WriteString(`</div>`)
		writeTelemetryTimeline(&b, report, r)
		if len(report.Gaps) > 0 {
			b.WriteString(`<article class="tile"><h2>Пробелы в данных</h2><ul>`)
			for _, gap := range report.Gaps {
				fmt.Fprintf(&b, `<li>%s</li>`, esc(gap))
			}
			b.WriteString(`</ul></article>`)
		}
		fmt.Fprintf(&b, `<p class="foot">Полные производные данные: <a href="/api/reports/telemetry/%s">JSON технического отчёта</a>. Исходная сессия остаётся в локальном хранилище.</p>`, esc(id))
	}
	b.WriteString(`<h2>Записано в текущем хранилище</h2>`)
	if hookErr == nil {
		writeHookReport(&b, hook)
	} else if errors.Is(hookErr, os.ErrNotExist) {
		b.WriteString(`<article class="tile"><h2>Hooks</h2><p class="unknown">Нет записанных Hooks с этим ID в текущем локальном хранилище.</p></article>`)
	} else {
		b.WriteString(`<article class="tile"><h2>Hooks</h2><p class="unknown">Источник сейчас недоступен.</p></article>`)
	}
	if otelErr == nil {
		writeOTelReport(&b, otel)
	} else {
		b.WriteString(`<article class="tile"><h2>OTel</h2><p class="unknown">Источник сейчас недоступен.</p></article>`)
	}
	render(w, "Технический отчёт", "sessions", safeHTML(b.String()))
}

func writeDeliveryReport(b *strings.Builder, delivery DeliveryReport) {
	b.WriteString(`<article class="tile"><div class="tile-head"><h2>Доставка событий сборщика</h2><span class="tag blue">локальная диагностика</span></div>`)
	if !delivery.QueueAvailable {
		b.WriteString(`<p class="unknown">Локальный буфер сборщика сейчас недоступен; отсутствие записанных Hooks не означает отсутствие событий.</p>`)
	} else {
		fmt.Fprintf(b, `<p>%d событий с этим ID ожидают отправки · %d байт</p>`, delivery.QueuedEvents, delivery.QueuedBytes)
		if delivery.OldestQueuedAt != "" {
			fmt.Fprintf(b, `<p class="helper">Самое раннее ожидающее событие: %s.</p>`, esc(delivery.OldestQueuedAt))
		}
		if delivery.UnreadableQueued > 0 {
			fmt.Fprintf(b, `<p class="unknown">%d файлов очереди не удалось прочитать; число ожидающих событий с этим ID может быть неполным.</p>`, delivery.UnreadableQueued)
		}
	}
	if !delivery.DiagnosticsAvailable {
		b.WriteString(`<p class="unknown">Журнал потерь и ошибок доставки ещё недоступен. Нулевое число записей в хранилище не показывает причину.</p>`)
	} else {
		fmt.Fprintf(b, `<p>Известных потерь при переполнении для этого ID: %d.</p>`, delivery.DroppedEvents)
		fmt.Fprintf(b, `<p class="helper">Диагностический журнал обновлён: %s.</p>`, esc(delivery.DiagnosticsUpdatedAt))
		if delivery.LastDroppedAt != "" {
			fmt.Fprintf(b, `<p class="helper">Последняя потеря: %s.</p>`, esc(delivery.LastDroppedAt))
		}
		if delivery.LastSendFailureAt != "" {
			fmt.Fprintf(b, `<p class="helper">Последняя ошибка отправки: %s.</p>`, esc(delivery.LastSendFailureAt))
		}
		if delivery.LastCollectorAcceptAt != "" {
			fmt.Fprintf(b, `<p class="helper">Последний ответ HTTP 2xx от коллектора: %s. Это ещё не доказательство записи в ClickHouse.</p>`, esc(delivery.LastCollectorAcceptAt))
		}
		if delivery.UnattributedDropped > 0 {
			fmt.Fprintf(b, `<p class="helper">Потери без ID сессии во всём локальном буфере: %d; приписать их этой сессии нельзя.</p>`, delivery.UnattributedDropped)
		}
	}
	b.WriteString(`<p class="helper">Диагностика показывает только наблюдаемые события текущего локального сборщика. Сбои записи в сам буфер и период до включения диагностики здесь не подсчитываются.</p>`)
	fmt.Fprintf(b, `<p><a href="/api/reports/delivery/%s">JSON доставки</a></p></article>`, esc(delivery.SessionID))
}

func writeTechnicalFindings(b *strings.Builder, findings []TechnicalFinding) {
	b.WriteString(`<article class="tile"><div class="tile-head"><h2>Выводы из Hooks и OTel</h2><span class="tag blue">только технические данные</span></div><p class="helper">Они описывают наличие и полноту записи. Исход задачи и качество работы оцениваются в Deep отдельно.</p><div class="finding-list">`)
	if len(findings) == 0 {
		b.WriteString(`<p class="unknown">Проверяемых технических выводов по доступным данным пока нет.</p>`)
	}
	for _, finding := range findings {
		fmt.Fprintf(b, `<div class="finding"><h3>%s</h3><p class="helper">Основание: %s</p><p>%s</p></div>`, esc(finding.Finding), esc(finding.Evidence), esc(finding.Meaning))
	}
	b.WriteString(`</div></article>`)
}

func writeHookReport(b *strings.Builder, hook HookReport) {
	fmt.Fprintf(b, `<article class="tile"><div class="tile-head"><h2>Записанные Hooks</h2><span class="tag green">записано</span></div><p>%d событий · %d вводов · %d начал вызовов · %d завершений</p>`, hook.Session.Events, hook.Session.Prompts, hook.Session.ToolStarts, hook.Session.ToolEnds)
	fmt.Fprintf(b, `<p class="helper">Период записи: %s — %s. Эти события не заменяют историческую телеметрию исходного периода.</p>`, esc(hook.Session.First), esc(hook.Session.Last))
	b.WriteString(`<p class="helper">Полнота PreToolUse/PostToolUse оценивается только для этого ID и периода. Неполная пара не доказывает, что вызов не завершился; причина, включая возможный запуск процесса до настройки Hooks, требует отдельной проверки.</p>`)
	for _, note := range hook.Coverage {
		fmt.Fprintf(b, `<p class="helper">%s</p>`, esc(note))
	}
	fmt.Fprintf(b, `<p><a href="/api/reports/hooks/%s">JSON Hooks</a></p>`, esc(hook.Session.ID))
	if len(hook.Events) > 0 {
		b.WriteString(`<div class="table-wrap"><table><thead><tr><th>Время</th><th>Событие</th><th>Инструмент</th><th>ID вызова</th></tr></thead><tbody>`)
		for _, ev := range hook.Events {
			fmt.Fprintf(b, `<tr><td class="mono">%s</td><td>%s</td><td>%s</td><td class="mono">%s</td></tr>`, esc(ev.At), esc(ev.Name), esc(ev.Tool), esc(ev.CallID))
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</article>`)
}

func writeOTelReport(b *strings.Builder, otel OTelReport) {
	badgeColor, badgeText := sourceColorIf(otel.hasData()), statusNameIf(otel.hasData())
	if !otel.Queried {
		badgeColor, badgeText = "outline", "не проверено"
	} else if otel.Coverage == "partial" {
		badgeColor, badgeText = "orange", "частичный охват"
	}
	fmt.Fprintf(b, `<article class="tile"><div class="tile-head"><h2>Связанные OTel записи</h2><span class="tag %s">%s</span></div>`, badgeColor, esc(badgeText))
	if otel.Queried {
		coverageLabel := "проверенный интервал"
		if otel.Coverage == "partial" {
			coverageLabel = "частичный охват"
		}
		fmt.Fprintf(b, `<p>%d логов · %d трейсов · %d записей метрик в проверенном интервале</p><p class="helper">%s: %s — %s.</p>`, otel.logCount(), otel.Traces, otel.MetricRecords, esc(coverageLabel), esc(otel.WindowStart), esc(otel.WindowEnd))
	}
	fmt.Fprintf(b, `<p class="helper">%s</p>`, esc(otel.CoverageDetail))
	b.WriteString(`<p class="helper">Связь установлена только по точному ID сессии. Глобальные traces и metrics без такого ID не распределяются между сессиями.</p>`)
	if len(otel.LogGroups) > 0 {
		b.WriteString(`<div class="table-wrap"><table><thead><tr><th>Сервис</th><th>Событие</th><th>Количество</th><th>Первое</th><th>Последнее</th></tr></thead><tbody>`)
		for _, group := range otel.LogGroups {
			fmt.Fprintf(b, `<tr><td>%s</td><td>%s</td><td class="num">%d</td><td class="mono">%s</td><td class="mono">%s</td></tr>`, esc(group.Service), esc(group.EventName), group.Events, esc(group.FirstEvent), esc(group.LastEvent))
		}
		b.WriteString(`</tbody></table></div>`)
	} else if otel.Queried && !otel.hasData() {
		b.WriteString(`<p class="unknown">В указанном проверенном интервале записей с этим ID не найдено.</p>`)
	}
	fmt.Fprintf(b, `<p><a href="/api/reports/otel/%s">JSON OTel</a></p></article>`, esc(otel.SessionID))
}

func sourceColorIf(found bool) string {
	if found {
		return "green"
	}
	return "outline"
}
func statusNameIf(found bool) string {
	if found {
		return "записано"
	}
	return "нет связанных данных"
}

func writeTelemetryMetrics(b *strings.Builder, report TelemetryReport) {
	b.WriteString(`<article class="tile span-12"><div class="tile-head"><h2>Что можно посчитать</h2><span class="label">из доступных источников</span></div><div class="metric-grid">`)
	keys := []string{"turns_started", "turns_completed", "user_messages", "tool_calls", "tool_results", "context_compactions", "token_input", "token_output", "token_total", "cost_usd", "api_latency_ms", "quality_score"}
	for _, key := range keys {
		metric, ok := report.Metrics[key]
		if !ok {
			continue
		}
		value := strings.TrimSpace(string(metric.Value))
		if value == "" || value == "null" {
			value = "нет данных"
		}
		value = strings.Trim(value, `"`)
		fmt.Fprintf(b, `<div class="metric"><span class="helper">%s</span><strong>%s</strong><span class="helper">%s · %s · %s</span>`, esc(metricName(key)), esc(value), esc(unitName(metric.Unit)), esc(sourceName(metric.Source)), esc(provenanceName(metric.Provenance)))
		if metric.Detail != "" {
			fmt.Fprintf(b, `<span class="helper">%s</span>`, esc(metric.Detail))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></article>`)
}

func writeTelemetryTimeline(b *strings.Builder, report TelemetryReport, r *http.Request) {
	counts := map[string]int{}
	for _, ev := range report.Events {
		counts[ev.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if counts[kinds[i]] != counts[kinds[j]] {
			return counts[kinds[i]] > counts[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	max := 1
	if len(kinds) > 0 {
		max = counts[kinds[0]]
	}
	b.WriteString(`<article class="tile"><div class="tile-head"><h2>События по типам</h2><span class="label">количество</span></div><div class="chart">`)
	for _, kind := range kinds {
		width := counts[kind] * 100 / max
		fmt.Fprintf(b, `<div class="chart-row"><span>%s</span><div class="chart-track"><i style="width:%d%%"></i></div><strong>%d</strong></div>`, esc(eventName(kind)), width, counts[kind])
	}
	b.WriteString(`</div></article>`)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage := 150
	pages := (len(report.Events) + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > len(report.Events) {
		end = len(report.Events)
	}
	fmt.Fprintf(b, `<article class="tile"><div class="tile-head"><h2>Хронология</h2><span class="label">%d событий · страница %d из %d</span></div><p class="helper">События transcript восстановлены из локальной истории. Они не являются записанными Hooks или OTel.</p><div class="table-wrap"><table><thead><tr><th>Время</th><th>Событие</th><th>Источник</th><th>Доказательство</th></tr></thead><tbody>`, len(report.Events), page, pages)
	for _, ev := range report.Events[start:end] {
		at := "—"
		if ev.At != nil {
			at = *ev.At
		}
		evidence := evidenceLabel(ev.Evidence)
		fmt.Fprintf(b, `<tr><td class="mono">%s</td><td>%s`, esc(at), esc(eventName(ev.Kind)))
		if ev.Tool != "" {
			fmt.Fprintf(b, `<div class="sub mono">%s</div>`, esc(ev.Tool))
		}
		fmt.Fprintf(b, `</td><td><span class="tag %s">%s</span><div class="sub">%s</div></td><td class="mono">%s</td></tr>`, sourceColor(ev.Source), esc(sourceName(ev.Source)), esc(provenanceName(ev.Provenance)), esc(evidence))
	}
	b.WriteString(`</tbody></table></div><div class="pager">`)
	if page > 1 {
		fmt.Fprintf(b, `<a href="?page=%d">← Предыдущая</a>`, page-1)
	}
	if page < pages {
		fmt.Fprintf(b, `<a href="?page=%d">Следующая →</a>`, page+1)
	}
	b.WriteString(`</div></article>`)
}

func (s *server) deepPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.v2DeepExists(id) {
		report, err := s.v2Deep(id)
		if err != nil {
			serveError(w, err)
			return
		}
		s.deepPageV2(w, r, report)
		return
	}
	report, err := s.deep(id)
	if err != nil {
		serveError(w, err)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p><a href="/sessions/%s">← Сессия</a></p><p class="summary">Глубокий отчёт · <span class="mono">%s</span></p><div class="grid"><article class="tile span-8"><div class="tile-head"><h2>Задача и исход</h2><span class="tag %s">%s</span></div><p>%s</p><div class="flag-box"><strong>Основание вывода</strong><span>%s</span></div>`, esc(id), esc(id), outcomeColor(report.Outcome), esc(outcomeName(report.Outcome)), esc(report.Task), esc(report.OutcomeBasis))
	if report.ModelOpinion != "" {
		fmt.Fprintf(&b, `<p class="opinion">Мнение модели: %s</p>`, esc(report.ModelOpinion))
	}
	b.WriteString(`</article><article class="tile span-4"><h2>Охват анализа</h2>`)
	fmt.Fprintf(&b, `<p>%d наблюдений · %d предложений · %d неизвестных</p><p class="helper">Предложения остаются черновиками до отдельного решения и проверки после изменения.</p></article></div>`, len(report.Observations), len(report.Recommendations), len(report.Unknowns))
	b.WriteString(`<article class="tile"><h2>Выводы Deep</h2><div class="finding-list">`)
	if len(report.Observations) == 0 {
		b.WriteString(`<p class="unknown">Подтверждённых наблюдений пока нет.</p>`)
	}
	for _, o := range report.Observations {
		fmt.Fprintf(&b, `<div class="finding"><div class="tags"><span class="tag purple">%s</span><span class="tag %s">%s</span></div><p>%s</p><p class="helper">Доказательства: %s</p></div>`, esc(o.Pattern), statusColor(o.Status), esc(o.Status), esc(o.Finding), esc(strings.Join(o.Evidence, ", ")))
	}
	b.WriteString(`</div></article><div id="coverage"></div>`)
	coverage, coverageErr := s.coverage(id)
	if coverageErr == nil {
		writeCoverageDetail(&b, coverage)
	} else if errors.Is(coverageErr, os.ErrNotExist) {
		b.WriteString(`<article class="tile"><h2>Покрытие всех 13 проверок</h2><p class="unknown">Повторный разбор ещё не завершён. Статус всех проверок: не проверено.</p></article>`)
	} else {
		serveError(w, coverageErr)
		return
	}
	b.WriteString(`<article class="tile"><h2>Что изменить</h2><div class="recs">`)
	if len(report.Recommendations) == 0 {
		b.WriteString(`<p class="unknown">Пока нет предложений с доказательствами.</p>`)
	}
	for _, p := range report.Recommendations {
		fmt.Fprintf(&b, `<div class="card"><span class="tag blue">черновик</span><h3>%s</h3><p class="helper">%s</p><p class="change">%s</p><p class="helper">Доказательства: %s</p></div>`, esc(p.Action), esc(p.Target), esc(p.Change), esc(strings.Join(p.Evidence, ", ")))
	}
	b.WriteString(`</div></article>`)
	if len(report.Unknowns) > 0 {
		b.WriteString(`<article class="tile"><h2>Остаётся неизвестным</h2><ul>`)
		for _, unknown := range report.Unknowns {
			fmt.Fprintf(&b, `<li>%s</li>`, esc(unknown))
		}
		b.WriteString(`</ul></article>`)
	}
	fmt.Fprintf(&b, `<p class="foot"><a href="/api/reports/deep/%s">JSON глубокого отчёта</a></p>`, esc(id))
	render(w, "Глубокий отчёт", "sessions", safeHTML(b.String()))
}

func sourceName(v string) string {
	switch v {
	case "hook":
		return "Hooks"
	case "otel":
		return "OTel"
	case "transcript":
		return "Транскрипт"
	case "app":
		return "Ссылка Codex"
	default:
		return v
	}
}
func sourceColor(v string) string {
	switch v {
	case "hook", "otel":
		return "green"
	case "transcript":
		return "purple"
	default:
		return "blue"
	}
}
func provenanceName(v string) string {
	switch v {
	case "recorded":
		return "записано"
	case "reconstructed":
		return "восстановлено"
	default:
		return v
	}
}
func statusName(v string) string {
	switch v {
	case "available":
		return "доступно"
	case "partial":
		return "частично"
	case "missing", "unavailable":
		return "нет данных"
	case "separate_store":
		return "отдельный поток"
	default:
		return v
	}
}
func statusColor(v string) string {
	switch v {
	case "available", "verified":
		return "green"
	case "partial", "candidate":
		return "yellow"
	case "separate_store":
		return "blue"
	case "missing", "unavailable", "failed":
		return "red"
	default:
		return "gray"
	}
}
func outcomeName(v string) string {
	switch v {
	case "verified":
		return "подтверждён"
	case "partial":
		return "частичный"
	case "failed":
		return "не достигнут"
	default:
		return "неизвестен"
	}
}
func outcomeColor(v string) string {
	switch v {
	case "verified":
		return "green"
	case "partial":
		return "yellow"
	case "failed":
		return "red"
	default:
		return "outline"
	}
}
func eventName(v string) string {
	switch v {
	case "turn_started":
		return "Начало хода"
	case "turn_completed":
		return "Завершение хода"
	case "user_message":
		return "Запись с ролью user"
	case "assistant_message":
		return "Ответ агента"
	case "tool_call":
		return "Вызов инструмента"
	case "tool_result":
		return "Результат инструмента"
	case "context_compaction":
		return "Сжатие контекста"
	default:
		return v
	}
}
func metricName(v string) string {
	switch v {
	case "turns_started":
		return "Начато ходов"
	case "turns_completed":
		return "Завершено ходов"
	case "user_messages":
		return "Записей с ролью user"
	case "tool_calls":
		return "Вызовов инструментов"
	case "tool_results":
		return "Результатов инструментов"
	case "context_compactions":
		return "Сжатий контекста"
	case "token_input":
		return "Входные токены"
	case "token_output":
		return "Выходные токены"
	case "token_total":
		return "Токены по счётчику"
	case "cost_usd":
		return "Стоимость, USD"
	case "api_latency_ms":
		return "Задержка API"
	case "quality_score":
		return "Качество"
	default:
		return v
	}
}

func evidenceLabel(raw json.RawMessage) string {
	var reference struct {
		Source string `json:"source"`
		Line   int    `json:"line"`
	}
	if json.Unmarshal(raw, &reference) == nil && reference.Source == "local_transcript" && reference.Line > 0 {
		return fmt.Sprintf("Транскрипт · L%d", reference.Line)
	}
	return strings.TrimSpace(string(raw))
}

func unitName(v string) string {
	switch v {
	case "turns":
		return "ходов"
	case "messages":
		return "сообщений"
	case "calls":
		return "вызовов"
	case "results":
		return "результатов"
	case "tokens":
		return "токенов"
	case "ms":
		return "мс"
	case "USD":
		return "USD"
	default:
		return v
	}
}

func sourceSummary(source, status string) string {
	if status == "separate_store" {
		return "Записанный поток проверяется отдельно по точному ID сессии."
	}
	if status == "missing" || status == "unavailable" {
		switch source {
		case "hook":
			return "За исходный период сессии записанные Hooks не найдены."
		case "otel":
			return "За исходный период сессии связанные OTel данные не найдены."
		}
	}
	if status == "available" {
		switch source {
		case "transcript":
			return "Локальный журнал доступен; сырое содержимое не входит в технический отчёт."
		case "app":
			return "Ссылка на сессию открывается в Codex."
		}
	}
	return "Охват источника указан в подробностях."
}
