package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// maxTokenBytes — ответ длиннее считается не токеном (вставили не то).
const maxTokenBytes = 4096

// validateTimeout — весь проверочный POST целиком.
const validateTimeout = 10 * time.Second

// tokenStatus — вердикт коллектора о токене-кандидате.
type tokenStatus int

const (
	tokenUnknown  tokenStatus = iota // сеть, 5xx, редирект, частичный отказ: нельзя ни сохранять, ни считать отвергнутым
	tokenAccepted                    // 2xx без отказа в partialSuccess
	tokenRejected                    // 401/403
)

func (s tokenStatus) String() string {
	switch s {
	case tokenAccepted:
		return "accepted"
	case tokenRejected:
		return "rejected"
	}
	return "unknown"
}

// tokenOutcome — результат validateToken. Detail не содержит токена
// (вычищен), для accepted пуст.
type tokenOutcome struct {
	Status tokenStatus
	Detail string
}

// scrubToken заменяет токен в тексте, который может уйти пользователю или в лог.
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}

// validateToken проверяет кандидата POST'ом пустого OTLP-пакета на logsURL
// (…/v1/logs). Токен живёт только в памяти. Редиректы не выполняются: токен
// не уходит на другой адрес, а 3xx — это unknown. Контракт коллектора
// (наблюдался 2026-09-29): без токена 401, валидный токен и пустой пакет — 200.
func validateToken(ctx context.Context, logsURL, token string) tokenOutcome {
	unknown := func(format string, args ...any) tokenOutcome {
		return tokenOutcome{Status: tokenUnknown, Detail: scrubToken(fmt.Sprintf(format, args...), token)}
	}
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, logsURL, strings.NewReader(`{"resourceLogs":[]}`))
	if err != nil {
		return unknown("%s: %v", logsURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return unknown("%s: %v", logsURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return tokenOutcome{Status: tokenRejected, Detail: scrubToken(fmt.Sprintf("%s: HTTP %d", logsURL, resp.StatusCode), token)}
	case resp.StatusCode/100 == 3:
		return unknown("%s: HTTP %d, redirect refused", logsURL, resp.StatusCode)
	case resp.StatusCode/100 != 2:
		return unknown("%s: HTTP %d", logsURL, resp.StatusCode)
	}
	if n := partialRejected(body); n > 0 {
		return unknown("%s: HTTP %d, collector rejected %d log records", logsURL, resp.StatusCode, n)
	}
	return tokenOutcome{Status: tokenAccepted}
}

// partialRejected — partialSuccess.rejectedLogRecords из ответа OTLP/JSON.
// protobuf-JSON кодирует int64 строкой, но встречается и число.
func partialRejected(body []byte) int64 {
	var r struct {
		PartialSuccess struct {
			RejectedLogRecords json.RawMessage `json:"rejectedLogRecords"`
		} `json:"partialSuccess"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.PartialSuccess.RejectedLogRecords) == 0 {
		return 0
	}
	raw := strings.Trim(string(r.PartialSuccess.RejectedLogRecords), `"`)
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// acquireOutcome — чем кончилась попытка получить токен от пользователя.
type acquireOutcome int

const (
	acquireAccepted    acquireOutcome = iota // токен получен (ещё не проверен коллектором)
	acquireCancelled                         // пользователь нажал Cancel
	acquireUnavailable                       // диалог не показать (нет GUI, нет osascript)
	acquireTimeout                           // время ожидания вышло
	acquireInvalid                           // пустой ответ или длиннее maxTokenBytes
)

func (a acquireOutcome) String() string {
	switch a {
	case acquireAccepted:
		return "accepted"
	case acquireCancelled:
		return "cancelled"
	case acquireUnavailable:
		return "unavailable"
	case acquireTimeout:
		return "timeout"
	}
	return "invalid"
}

// tokenSource — откуда setup берёт токен. Токен никогда не приходит из
// аргументов инструмента и не проходит через чат.
type tokenSource interface {
	Ask(ctx context.Context) (string, acquireOutcome)
}

// dialogSource — системный диалог macOS со скрытым полем ввода.
type dialogSource struct {
	osascript string // пусто — /usr/bin/osascript
}

// dialogGaveUpMarker — текст ошибки, которой обработчик сообщает «время вышло».
const dialogGaveUpMarker = "hottell: dialog gave up"

// dialogHandler разбирает запись ответа display dialog: «время вышло»
// проверяется до чтения текста, поэтому недописанный ввод не уходит в stdout.
const dialogHandler = `on answer(r)
	if gave up of r then error "` + dialogGaveUpMarker + `" number 1
	return text returned of r
end answer`

// dialogCall — 45 с меньше таймаута MCP-вызова Codex по умолчанию (60 с).
const dialogCall = `answer(display dialog "Enter the hottell ingest token" default answer "" with hidden answer with title "hottell" giving up after 45)`

// dialogDeadline — страховка, если osascript завис сверх giving up after.
const dialogDeadline = 50 * time.Second

// Ask показывает один диалог. Токен читается из stdout osascript и в argv не попадает.
func (d dialogSource) Ask(ctx context.Context) (string, acquireOutcome) {
	bin := d.osascript
	if bin == "" {
		bin = "/usr/bin/osascript"
	}
	ctx, cancel := context.WithTimeout(ctx, dialogDeadline)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-e", dialogHandler, "-e", dialogCall)
	// A cancelled shell can leave a child holding stdout/stderr open. Bound the
	// pipe wait so cancellation returns promptly even in that case.
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return "", acquireTimeout
	case ctx.Err() != nil:
		return "", acquireCancelled
	case err != nil && strings.Contains(stderr.String(), dialogGaveUpMarker):
		return "", acquireTimeout
	case err != nil && strings.Contains(stderr.String(), "(-128)"):
		return "", acquireCancelled
	case err != nil:
		return "", acquireUnavailable
	}
	tok := strings.TrimSpace(stdout.String())
	if tok == "" || len(tok) > maxTokenBytes {
		return "", acquireInvalid
	}
	return tok, acquireAccepted
}

// writeSecretFile атомарно пишет файл с секретом: уникальный временный файл
// в том же каталоге (0600), rename, затем chmod 0600 — итог 0600, даже если
// файл существовал с более широкими правами. Недостающие каталоги создаются
// 0700; права существующих каталогов не меняются.
func writeSecretFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".hottell-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}

// saveToken — файл токена: каталог 0700 (и существующий тоже), файл 0600.
func saveToken(path, token string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return writeSecretFile(path, []byte(token+"\n"))
}

// isTerminal — stdin это терминал (символьное устройство), а не пайп или файл.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// readTokenLine читает одну строку; с терминала — с выключенным эхом,
// которое возвращается на любом выходе, включая Ctrl-C.
func readTokenLine(stdin *os.File, stderr io.Writer) (string, error) {
	if isTerminal(stdin) {
		stty := func(arg string) {
			c := exec.Command("stty", arg)
			c.Stdin = stdin
			_ = c.Run()
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		done := make(chan struct{})
		defer func() {
			signal.Stop(sig)
			close(done)
			stty("echo")
			fmt.Fprintln(stderr)
		}()
		go func() {
			select {
			case <-sig:
				stty("echo")
				fmt.Fprintln(stderr)
				os.Exit(130)
			case <-done:
			}
		}()
		stty("-echo")
		fmt.Fprint(stderr, "token: ")
	}
	line, err := bufio.NewReader(io.LimitReader(stdin, maxTokenBytes+2)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// runTokenCmd — `hottell token`: токен со stdin, проверка коллектором,
// сохранение только при приёме. Печатает только исход; токен — никогда.
func runTokenCmd(ctx context.Context, stdin *os.File, stdout, stderr io.Writer, cfg Config) int {
	tok, err := readTokenLine(stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "token: не прочитан:", err)
		return 1
	}
	if tok == "" || len(tok) > maxTokenBytes {
		fmt.Fprintf(stderr, "token: пустой или длиннее %d байт — не сохранён\n", maxTokenBytes)
		return 1
	}
	out := validateToken(ctx, cfg.Endpoint, tok)
	switch out.Status {
	case tokenRejected:
		fmt.Fprintln(stderr, "token: коллектор отверг токен — не сохранён:", out.Detail)
		return 1
	case tokenUnknown:
		fmt.Fprintln(stderr, "token: коллектор не подтвердил токен — не сохранён:", out.Detail)
		return 1
	}
	if err := saveToken(cfg.TokenFile, tok); err != nil {
		fmt.Fprintln(stderr, "token: принят, но не сохранён:", scrubToken(err.Error(), tok))
		return 1
	}
	fmt.Fprintln(stdout, "token: принят и сохранён в", cfg.TokenFile)
	return 0
}
