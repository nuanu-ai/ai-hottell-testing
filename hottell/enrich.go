package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Обогащение событий Codex фактами из rollout сессии. Хуки Codex не несут
// того, что нужнее всего для анализа: PostToolUse для shell-команды — голый
// вывод без кода выхода, Stop — без токенов хода. Оба факта лежат в rollout
// (`transcript_path` из payload хука). Читает их дрейнер, не хук: горячий путь
// остаётся коротким. Копируются только числа, статусы и имя модели — текст
// команд, выводы и сообщения никогда.
//
// Чтение — только хвост файла, назад, растущими кусками до потолка:
// rollout бывает в сотни мегабайт, а нужные записи почти всегда в конце.

// Переменные, а не константы, — чтобы тесты могли сжать окна; конфигом не
// управляются.
var (
	enrichFirstChunk int64 = 1 << 20
	enrichMaxChunk   int64 = 4 << 20 // крупнее — лишняя память без выигрыша в скорости
	enrichReadCap    int64 = 64 << 20
	// enrichGrace — сколько событие может ждать записи в rollout: запись
	// появляется чуть позже хука (task_complete — через десятки мс после Stop).
	enrichGrace     = 5 * time.Second
	enrichRetryBase = 250 * time.Millisecond
)

const (
	enrichFound        = "found"
	enrichPartial      = "partial"
	enrichNotFound     = "not_found"
	enrichNoTranscript = "no_transcript"
	enrichDisabled     = "disabled"
	enrichError        = "error"

	keyEnrichStatus = "hottell.enrich_status"
)

type enrichOutcome struct {
	hold    bool // запись ещё не в rollout: спул-файл остаётся до следующего круга
	changed bool // payload получил атрибуты обогащения
}

func enrichable(ev event) bool {
	return ev.Agent == "codex" && (ev.Event == "PostToolUse" || ev.Event == "Stop") && ev.Payload != nil
}

// enrichEvents дополняет payload событий Codex PostToolUse/Stop на месте.
// Каждый rollout читается один раз на батч. Ошибки обогащения не мешают
// доставке: событие уходит со статусом, а не теряется.
func enrichEvents(cfg Config, events []event, now time.Time) []enrichOutcome {
	out := make([]enrichOutcome, len(events))
	byPath := map[string][]int{}
	var order []string
	for i := range events {
		ev := events[i]
		if !enrichable(ev) {
			continue
		}
		if _, done := ev.Payload[keyEnrichStatus]; done {
			continue // уже обогащено в прошлой попытке отправки
		}
		out[i].changed = true
		if !cfg.EnrichTranscript {
			ev.Payload[keyEnrichStatus] = enrichDisabled
			continue
		}
		p, _ := ev.Payload["transcript_path"].(string)
		if p == "" {
			ev.Payload[keyEnrichStatus] = enrichNoTranscript
			continue
		}
		if _, seen := byPath[p]; !seen {
			order = append(order, p)
		}
		byPath[p] = append(byPath[p], i)
	}
	for _, p := range order {
		enrichFromRollout(cfg, p, events, byPath[p], now, out)
	}
	return out
}

func eventYoung(ev event, now time.Time) bool {
	age := now.Sub(time.Unix(0, ev.TS))
	return age > -enrichGrace && age < enrichGrace
}

func enrichFromRollout(cfg Config, rawPath string, events []event, idxs []int, now time.Time, out []enrichOutcome) {
	setAll := func(status string) {
		for _, i := range idxs {
			events[i].Payload[keyEnrichStatus] = status
		}
	}
	path, status := rolloutPath(rawPath)
	if status != "" {
		setAll(status)
		return
	}
	f, err := openRollout(path)
	if err != nil {
		cfg.debugf("enrich: %v", err)
		if errors.Is(err, fs.ErrNotExist) {
			setAll(enrichNoTranscript)
		} else {
			setAll(enrichError)
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		setAll(enrichError)
		return
	}

	scan := newRolloutScan()
	for _, i := range idxs {
		p := events[i].Payload
		turn, _ := p["turn_id"].(string)
		switch events[i].Event {
		case "PostToolUse":
			if id, _ := p["tool_use_id"].(string); id != "" {
				scan.wantTool(id, turn)
			}
		case "Stop":
			if turn != "" {
				scan.wantTurn(turn)
			}
		}
	}
	var capHit bool
	var readErr error
	if scan.openTools+scan.openTurns > 0 {
		capHit, readErr = scanLinesBackward(f, info.Size(), scan.handle)
		if readErr != nil {
			cfg.debugf("enrich: чтение %s: %v", filepath.Base(path), readErr)
		}
		scan.finish(capHit)
	}

	for _, i := range idxs {
		ev := events[i]
		p := ev.Payload
		turn, _ := p["turn_id"].(string)
		switch ev.Event {
		case "PostToolUse":
			id, _ := p["tool_use_id"].(string)
			n := scan.tools[id]
			switch {
			case n != nil && n.found:
				n.apply(p)
			case readErr != nil:
				p[keyEnrichStatus] = enrichError
			default:
				p[keyEnrichStatus] = enrichNotFound
				out[i].hold = n != nil && eventYoung(ev, now)
			}
		case "Stop":
			t := scan.turns[turn]
			switch {
			case t != nil && t.started:
				t.apply(p, enrichFound)
				// task_complete пишется после хука Stop: подождём его ради duration.
				out[i].hold = !t.ended && eventYoung(ev, now)
			case t != nil && t.seen:
				t.apply(p, enrichPartial)
				out[i].hold = !t.ended && eventYoung(ev, now)
			case readErr != nil:
				p[keyEnrichStatus] = enrichError
			default:
				p[keyEnrichStatus] = enrichNotFound
				out[i].hold = t != nil && eventYoung(ev, now)
			}
		}
	}
}

// rolloutPath пропускает только обычный *.jsonl внутри ~/.codex: путь приходит
// из payload хука, и дрейнер не должен становиться читателем произвольных
// файлов. Симлинки раскрываются до проверки вложенности.
func rolloutPath(p string) (string, string) {
	if p == "" {
		return "", enrichNoTranscript
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Ext(p) != ".jsonl" {
		return "", enrichError
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", enrichError
	}
	root := filepath.Join(home, ".codex")
	if !pathWithin(root, p) {
		return "", enrichError
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", enrichNoTranscript
	}
	real, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", enrichNoTranscript
	}
	if err != nil || !pathWithin(realRoot, real) {
		return "", enrichError
	}
	info, err := os.Lstat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", enrichError
	}
	return real, ""
}

func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// openRollout: O_NOFOLLOW — последний компонент уже раскрыт и должен остаться
// файлом; O_NONBLOCK — чтобы подменённый FIFO не повесил дрейнер на open.
func openRollout(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// scanLinesBackward отдаёт fn целые строки от конца файла к началу, читая
// кусками 1 МБ, затем ×4 до enrichMaxChunk за раз, не больше enrichReadCap
// всего. Первая строка куска может быть неполной — она доклеивается к
// следующему (более раннему) куску. fn возвращает false, когда всё найдено.
// capHit — упёрлись в потолок, а непрочитанное осталось.
func scanLinesBackward(r io.ReaderAt, size int64, fn func(line []byte) bool) (capHit bool, err error) {
	off, read, chunk := size, int64(0), enrichFirstChunk
	var carry []byte // хвост строки, начало которой ещё не прочитано
	for off > 0 {
		if read >= enrichReadCap {
			return true, nil
		}
		n := min(chunk, off, enrichReadCap-read)
		buf := make([]byte, int(n)+len(carry))
		if m, err := r.ReadAt(buf[:n], off-n); int64(m) < n {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF // файл укоротили под нами
			}
			return false, err
		}
		copy(buf[n:], carry)
		off -= n
		read += n
		chunk = min(chunk*4, enrichMaxChunk)
		lines := buf
		carry = nil
		if off > 0 {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				carry = buf // строка длиннее куска: читаем дальше
				continue
			}
			carry, lines = buf[:i], buf[i+1:]
		}
		for end := len(lines); end > 0; {
			i := bytes.LastIndexByte(lines[:end], '\n')
			if line := lines[i+1 : end]; len(line) > 0 {
				if !fn(line) {
					return false, nil
				}
			}
			end = i
		}
	}
	return false, nil
}

// ---------- разбор записей rollout ----------

type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

func (t *codexTokens) add(o codexTokens) {
	t.Input += o.Input
	t.Cached += o.Cached
	t.CacheWrite += o.CacheWrite
	t.Output += o.Output
	t.Reasoning += o.Reasoning
	t.Total += o.Total
}

// rolloutRecord — только нужные поля; большие (stdout, аргументы, тексты)
// декодер пропускает, не создавая строк.
type rolloutRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type          string          `json:"type"`
		TurnID        string          `json:"turn_id"`
		CallID        string          `json:"call_id"`
		Model         string          `json:"model"`
		ResponseID    string          `json:"response_id"`
		DurationMs    any             `json:"duration_ms"`
		StartedAtMs   any             `json:"started_at_ms"`
		CompletedAtMs any             `json:"completed_at_ms"`
		Output        json.RawMessage `json:"output"`
		Item          *struct {
			ID         string `json:"id"`
			Status     any    `json:"status"`
			ExitCode   any    `json:"exit_code"`
			Duration   any    `json:"duration"`
			DurationMs any    `json:"durationMs"`
		} `json:"item"`
		Usage     *codexTokens `json:"usage"`
		TurnUsage *codexTokens `json:"turn_token_usage"`
		Info      *struct {
			Total *codexTokens `json:"total_token_usage"`
			Last  *codexTokens `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

type toolNeed struct {
	turn       string
	needle     []byte // id в кавычках: поиск по сырой строке
	found      bool   // найдена запись о завершении: item_completed или вывод вызова
	final      bool   // раньше в файле про этот вызов ничего быть не может
	status     string
	exitCode   *int64
	durationMs *int64
}

type tokenCount struct{ total, last codexTokens }

type turnNeed struct {
	seen, started, ended, done bool
	waitBaseline, baselineDone bool
	model                      string
	durationMs                 *int64
	records                    int
	respIDs                    map[string]bool
	sum                        codexTokens
	turnUsage                  *codexTokens // turn_token_usage последней записи хода
	counts                     []tokenCount // старый формат, от новых к старым
	baseline                   *codexTokens // total последнего token_count до начала хода
}

type rolloutScan struct {
	tools     map[string]*toolNeed
	turns     map[string]*turnNeed
	openTools int
	openTurns int

	// token_count и turn_context без turn_id — с последней границы хода
	// (task_started/task_complete/turn_aborted), от новых к старым.
	pending      []tokenCount
	pendingModel string
	lastEndOf    string // ход, чей конец был последней встреченной границей
}

func newRolloutScan() *rolloutScan {
	return &rolloutScan{tools: map[string]*toolNeed{}, turns: map[string]*turnNeed{}}
}

func (s *rolloutScan) wantTool(id, turn string) {
	if _, ok := s.tools[id]; ok {
		return
	}
	s.tools[id] = &toolNeed{turn: turn, needle: []byte(strconv.Quote(id))}
	s.openTools++
}

func (s *rolloutScan) wantTurn(id string) {
	if _, ok := s.turns[id]; ok {
		return
	}
	s.turns[id] = &turnNeed{respIDs: map[string]bool{}}
	s.openTurns++
}

func (s *rolloutScan) finalTool(n *toolNeed) {
	if !n.final {
		n.final = true
		s.openTools--
	}
}

func (s *rolloutScan) doneTurn(t *turnNeed) {
	if !t.done {
		t.done = true
		s.openTurns--
	}
}

var (
	needleTaskStarted  = []byte(`task_started`)
	needleTaskComplete = []byte(`task_complete`)
	needleTurnAborted  = []byte(`turn_aborted`)
	needleUsageRecord  = []byte(`token_usage_record`)
	needleTurnContext  = []byte(`turn_context`)
	needleTokenCount   = []byte(`token_count`)
)

const (
	// Служебные записи хода маленькие; у больших строк (выводы, скриншоты)
	// тип смотрится только в начале — Codex пишет "type" первым полем.
	smallLineBytes = 64 << 10
	headBytes      = 512
)

// handle — одна строка rollout, от новых к старым. Дешёвый префильтр по
// байтам; JSON разбирается только у записей, которые могут быть нужны.
func (s *rolloutScan) handle(raw []byte) bool {
	probe := raw
	if len(probe) > smallLineBytes {
		probe = probe[:headBytes]
	}
	boundary := bytes.Contains(probe, needleTaskStarted) || bytes.Contains(probe, needleTaskComplete) || bytes.Contains(probe, needleTurnAborted)
	turnLine := len(s.turns) > 0 && (bytes.Contains(probe, needleUsageRecord) || bytes.Contains(probe, needleTurnContext) || bytes.Contains(probe, needleTokenCount))
	toolLine := s.openTools > 0 && s.mentionsOpenTool(raw)
	if !boundary && !turnLine && !toolLine {
		return true
	}
	var rec rolloutRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return true // битая или недописанная строка
		}
	}
	p := &rec.Payload
	switch rec.Type {
	case "token_usage_record":
		s.usageRecord(&rec)
	case "turn_context":
		s.turnContext(p.TurnID, p.Model)
	case "event_msg":
		switch p.Type {
		case "task_started":
			s.taskStarted(p.TurnID)
		case "task_complete", "turn_aborted":
			s.taskEnded(p.TurnID, p.DurationMs)
		case "token_count":
			if p.Info != nil && p.Info.Total != nil {
				c := tokenCount{total: *p.Info.Total}
				if p.Info.Last != nil {
					c.last = *p.Info.Last
				}
				s.tokenCount(c)
			}
		case "item_completed":
			s.itemCompleted(&rec)
		}
	case "response_item":
		switch p.Type {
		case "function_call_output", "custom_tool_call_output":
			s.callOutput(p.CallID, p.Output)
		case "function_call", "custom_tool_call", "local_shell_call":
			if n := s.tools[p.CallID]; n != nil {
				// Вызов раньше своего item_completed и вывода: всё, что могло
				// быть про него, уже прочитано.
				s.finalTool(n)
			}
		}
	}
	return s.openTools > 0 || s.openTurns > 0
}

func (s *rolloutScan) mentionsOpenTool(raw []byte) bool {
	for _, n := range s.tools {
		if !n.final && bytes.Contains(raw, n.needle) {
			return true
		}
	}
	return false
}

func (s *rolloutScan) itemCompleted(rec *rolloutRecord) {
	it := rec.Payload.Item
	if it == nil {
		return
	}
	n := s.tools[it.ID]
	if n == nil || n.final {
		return
	}
	n.found = true
	if st := statusToken(it.Status); st != "" {
		n.status = st
	}
	if code, ok := wholeNumber(it.ExitCode); ok {
		n.exitCode = &code
	}
	if ms, ok := durationObjectMs(it.Duration); ok {
		n.durationMs = &ms
	} else if ms, ok := wholeNumber(it.DurationMs); ok {
		n.durationMs = &ms
	} else if start, ok1 := wholeNumber(rec.Payload.StartedAtMs); ok1 {
		if end, ok2 := wholeNumber(rec.Payload.CompletedAtMs); ok2 && end >= start {
			ms := end - start
			n.durationMs = &ms
		}
	}
	s.finalTool(n)
}

func (s *rolloutScan) callOutput(callID string, output json.RawMessage) {
	n := s.tools[callID]
	if n == nil || n.final || n.found {
		return
	}
	// Запасной путь для старых rollout и инструментов без item_completed:
	// код выхода из заголовка вывода. item_completed, если он есть, лежит
	// раньше вывода и перекроет эти значения.
	n.found = true
	if code, ms, ok := exitFromOutput(output); ok {
		n.exitCode = &code
		if ms >= 0 {
			n.durationMs = &ms
		}
	}
}

func (s *rolloutScan) usageRecord(rec *rolloutRecord) {
	p := &rec.Payload
	t := s.turns[p.TurnID]
	if t == nil || p.Usage == nil {
		return
	}
	t.seen = true
	if p.ResponseID != "" {
		if t.respIDs[p.ResponseID] {
			return
		}
		t.respIDs[p.ResponseID] = true
	}
	t.records++
	t.sum.add(*p.Usage)
	if t.turnUsage == nil && p.TurnUsage != nil {
		u := *p.TurnUsage
		t.turnUsage = &u
	}
}

func (s *rolloutScan) turnContext(turnID, model string) {
	if turnID == "" {
		if s.pendingModel == "" {
			s.pendingModel = model
		}
		return
	}
	if t := s.turns[turnID]; t != nil {
		t.seen = true
		if t.model == "" {
			t.model = model
		}
	}
}

func (s *rolloutScan) tokenCount(c tokenCount) {
	// Первый token_count до начала хода — точка отсчёта для дедупликации:
	// сразу после task_started Codex повторяет прошлый total_token_usage.
	for _, t := range s.turns {
		if t.waitBaseline && !t.baselineDone {
			total := c.total
			t.baseline, t.baselineDone = &total, true
			s.doneTurn(t)
		}
	}
	s.pending = append(s.pending, c)
}

func (s *rolloutScan) taskStarted(turnID string) {
	for _, n := range s.tools {
		if !n.final && n.turn != "" && n.turn == turnID {
			s.finalTool(n) // вызов не может быть раньше начала своего хода
		}
	}
	if t := s.turns[turnID]; t != nil && !t.started {
		t.seen, t.started = true, true
		t.counts = s.pending
		if t.model == "" {
			t.model = s.pendingModel
		}
		if t.records > 0 || len(t.counts) == 0 {
			s.doneTurn(t) // точка отсчёта нужна только старому формату
		} else {
			t.waitBaseline = true
		}
	}
	s.resetPending("")
}

func (s *rolloutScan) taskEnded(turnID string, duration any) {
	if t := s.turns[turnID]; t != nil && !t.ended {
		t.seen, t.ended = true, true
		if ms, ok := wholeNumber(duration); ok {
			t.durationMs = &ms
		}
	}
	s.resetPending(turnID)
}

func (s *rolloutScan) resetPending(endOf string) {
	s.pending, s.pendingModel, s.lastEndOf = nil, "", endOf
}

// finish: упёрлись в потолок посреди хода, конец которого уже встречен, —
// накопленные token_count и модель принадлежат этому ходу (частично).
func (s *rolloutScan) finish(capHit bool) {
	if !capHit || s.lastEndOf == "" {
		return
	}
	if t := s.turns[s.lastEndOf]; t != nil && !t.started {
		t.counts = s.pending
		if t.model == "" {
			t.model = s.pendingModel
		}
	}
}

// ---------- атрибуты ----------

func (n *toolNeed) apply(p map[string]any) {
	p[keyEnrichStatus] = enrichFound
	if n.status != "" {
		p["hottell.tool_status"] = n.status
	}
	if n.exitCode != nil {
		p["hottell.exit_code"] = *n.exitCode
	}
	if n.durationMs != nil {
		p["hottell.tool_duration_ms"] = *n.durationMs
	}
}

// tokens — ответы модели и токены хода. Новый формат: token_usage_record с
// turn_id; сумма — turn_token_usage последней записи (он накопительный и цел
// даже при частичном чтении), иначе сумма usage. Старый формат: last_token_usage
// из token_count внутри хода, повторы того же total_token_usage — опросы, не
// ответы.
func (t *turnNeed) tokens() (int, codexTokens) {
	if t.records > 0 {
		if t.turnUsage != nil {
			return t.records, *t.turnUsage
		}
		return t.records, t.sum
	}
	var sum codexTokens
	responses := 0
	prev := t.baseline
	for i := len(t.counts) - 1; i >= 0; i-- {
		c := t.counts[i]
		if prev != nil && *prev == c.total {
			continue
		}
		responses++
		sum.add(c.last)
		total := c.total
		prev = &total
	}
	return responses, sum
}

func (t *turnNeed) apply(p map[string]any, status string) {
	p[keyEnrichStatus] = status
	responses, u := t.tokens()
	p["hottell.turn.responses"] = int64(responses)
	p["hottell.turn.input_tokens"] = u.Input
	p["hottell.turn.cached_input_tokens"] = u.Cached
	p["hottell.turn.output_tokens"] = u.Output
	p["hottell.turn.reasoning_output_tokens"] = u.Reasoning
	if m := modelToken(t.model); m != "" {
		p["hottell.turn.model"] = m
	}
	if t.durationMs != nil {
		p["hottell.turn.duration_ms"] = *t.durationMs
	}
}

// ---------- значения ----------

func wholeNumber(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	}
	return 0, false
}

// durationObjectMs — Rust Duration {secs, nanos} в миллисекунды.
func durationObjectMs(v any) (int64, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	secs, ok1 := wholeNumber(m["secs"])
	nanos, ok2 := wholeNumber(m["nanos"])
	if !ok1 && !ok2 {
		return 0, false
	}
	return secs*1000 + nanos/1_000_000, true
}

// statusToken/modelToken — только короткие идентификаторы: в атрибуты не
// должен попасть произвольный текст из rollout.
func statusToken(v any) string {
	s, _ := v.(string)
	if len(s) == 0 || len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return s
}

func modelToken(s string) string {
	if len(s) == 0 || len(s) > 128 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:/-@", r)) {
			return ""
		}
	}
	return s
}

var (
	reExitCode      = regexp.MustCompile(`(?m)^Exit code: (-?\d+)\s*$`)
	reProcessExited = regexp.MustCompile(`(?m)^Process exited with code (-?\d+)\s*$`)
	reWallTime      = regexp.MustCompile(`(?m)^Wall time:? (\d+(?:\.\d+)?) seconds\s*$`)
)

// exitFromOutput — код выхода из вывода вызова (старые rollout): заголовок
// "Exit code: N" / "Process exited with code N" до строки "Output:" или
// JSON {"output":…,"metadata":{"exit_code":N,"duration_seconds":S}}.
// Тело вывода не просматривается, чтобы не принять чужой текст за код.
func exitFromOutput(raw json.RawMessage) (code, durationMs int64, ok bool) {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return 0, -1, false
	}
	var text string
	switch x := v.(type) {
	case string:
		text = x
	case []any:
		for _, b := range x {
			if m, _ := b.(map[string]any); m != nil {
				if s, _ := m["text"].(string); s != "" {
					text = s
					break
				}
			}
		}
	case map[string]any:
		return exitFromMetadata(x)
	}
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(t), &obj) == nil {
			return exitFromMetadata(obj)
		}
	}
	header := text
	if i := strings.Index(header, "\nOutput:"); i >= 0 {
		header = header[:i]
	} else if len(header) > 2048 {
		header = header[:2048]
	}
	m := reExitCode.FindStringSubmatch(header)
	if m == nil {
		m = reProcessExited.FindStringSubmatch(header)
	}
	if m == nil {
		return 0, -1, false
	}
	code, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, -1, false
	}
	durationMs = -1
	if w := reWallTime.FindStringSubmatch(header); w != nil {
		if sec, err := strconv.ParseFloat(w[1], 64); err == nil {
			durationMs = int64(sec * 1000)
		}
	}
	return code, durationMs, true
}

func exitFromMetadata(obj map[string]any) (int64, int64, bool) {
	meta, _ := obj["metadata"].(map[string]any)
	code, ok := wholeNumber(meta["exit_code"])
	if !ok {
		return 0, -1, false
	}
	durationMs := int64(-1)
	if sec, isNum := meta["duration_seconds"].(float64); isNum && sec >= 0 {
		durationMs = int64(sec * 1000)
	}
	return code, durationMs, true
}
