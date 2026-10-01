package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sync"
	"time"
)

// Статус отправки в сервис команды: раз в минуту `hottell status --json`, последний итог —
// в GET /api/status. Ключ MCP и токен коллектора hottell status не печатает, поэтому
// вывод отдаётся как есть; запрос не с loopback отклоняется.

// sendStatusTimeout bounds one hottell status: it asks launchd about the daemon.
const sendStatusTimeout = 20 * time.Second

// sendStatusWaitDelay bounds the wait for the output after the deadline kills hottell: a child
// that keeps its stdout open would otherwise hold the check past the timeout.
const sendStatusWaitDelay = time.Second

type sendStatus struct {
	CheckedAt time.Time `json:"checked_at"`
	// State is ok, problem or not_configured (no binary, no answer in time, a run that failed
	// before the report, no MCP server, or a binary that does not print the status as JSON,
	// like the prototype).
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// ServiceOrigin is the origin of mcp.url: the links /telemetry and /connect lead there.
	ServiceOrigin string          `json:"service_origin,omitempty"`
	Status        json.RawMessage `json:"status,omitempty"`
}

type statusBox struct {
	mu   sync.Mutex
	last *sendStatus
}

func (b *statusBox) set(s sendStatus) { b.mu.Lock(); b.last = &s; b.mu.Unlock() }

func (b *statusBox) get() *sendStatus { b.mu.Lock(); defer b.mu.Unlock(); return b.last }

// checkSendStatus runs hottell status --json once and keeps the outcome.
func (s *server) checkSendStatus() {
	ctx, cancel := context.WithTimeout(context.Background(), sendStatusTimeout)
	defer cancel()
	out, err := hottellStatus(ctx, s.hottell)
	s.status.set(classifyStatus(out, err, time.Now()))
}

// hottellStatus runs hottell status --json. After the deadline the error is ctx.Err(), not the
// "signal: killed" of the process, so the reason can say it did not answer in time.
func hottellStatus(ctx context.Context, bin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	cmd.WaitDelay = sendStatusWaitDelay
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return out, err
}

func (s *server) watchSendStatus(every time.Duration) {
	s.checkSendStatus()
	for range time.Tick(every) {
		s.checkSendStatus()
	}
}

// classifyStatus reads hottell status --json. Exit 1 with JSON is a problem, not a failure.
func classifyStatus(out []byte, runErr error, now time.Time) sendStatus {
	st := sendStatus{CheckedAt: now.UTC(), State: "not_configured"}
	if errors.Is(runErr, context.DeadlineExceeded) {
		st.Reason = fmt.Sprintf("hottell status не ответил за %d с", int(sendStatusTimeout.Seconds()))
		return st
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		st.Reason = "бинаря hottell нет"
		if !errors.Is(runErr, fs.ErrNotExist) && !errors.Is(runErr, exec.ErrNotFound) {
			st.Reason = "hottell status не запустился: " + runErr.Error()
		}
		return st
	}
	var rep struct {
		OK  bool `json:"ok"`
		MCP struct {
			URL string `json:"url"`
		} `json:"mcp"`
	}
	if json.Unmarshal(out, &rep) != nil {
		st.Reason = "hottell status --json не дал JSON — бинарь не той версии"
		// Exit 1 and nothing on stdout: the new hottell failed before the report (the prototype
		// exits 2 with its usage).
		if exitErr != nil && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
			st.Reason = "hottell status завершился с ошибкой: " + firstLine(exitErr.Stderr, exitErr.Error())
		}
		return st
	}
	if rep.MCP.URL == "" {
		st.Reason = "MCP hottell не подключён"
		return st
	}
	st.Status = out
	if u, err := url.Parse(rep.MCP.URL); err == nil && u.Scheme != "" && u.Host != "" {
		st.ServiceOrigin = u.Scheme + "://" + u.Host
	}
	st.State = "problem"
	if rep.OK {
		st.State = "ok"
	}
	return st
}

// maxReasonLine caps, in runes, the line of stderr quoted in a reason.
const maxReasonLine = 200

// firstLine is the first non-empty line of b, cut to maxReasonLine runes with «…», or
// fallback when b has none.
func firstLine(b []byte, fallback string) string {
	for line := range bytes.Lines(b) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if r := []rune(string(line)); len(r) > maxReasonLine {
				return string(r[:maxReasonLine]) + "…"
			}
			return string(line)
		}
	}
	return fallback
}

func (s *server) sendStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !fromLoopback(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "только с этой машины"})
		return
	}
	st := s.status.get()
	if st == nil {
		writeJSON(w, http.StatusOK, map[string]string{"state": "unknown"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}
