package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hottellScript — исполняемый скрипт /bin/sh с телом body на месте бинаря hottell.
func hottellScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hottell")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeHottell — исполняемый скрипт, который печатает out и выходит с кодом code, как hottell status --json.
func fakeHottell(t *testing.T, out string, code int) string {
	t.Helper()
	return hottellScript(t, "cat <<'EOF'\n"+out+"\nEOF\nexit "+strconv.Itoa(code)+"\n")
}

// fakeHottellStderr печатает msg только в stderr и выходит с кодом code — как прототип или упавший hottell.
func fakeHottellStderr(t *testing.T, msg string, code int) string {
	t.Helper()
	return hottellScript(t, "cat >&2 <<'EOF'\n"+msg+"\nEOF\nexit "+strconv.Itoa(code)+"\n")
}

const statusConnected = `{"ok":true,"version":"0.2.3","mcp":{"agent":"claude","url":"http://localhost:8080/mcp"},
"token":{"present":true,"last_sent":"2026-10-01T06:58:00Z"},"settings":{"cached":true,"version":7,"applied":7},
"queue":{"queued":0},"problems":[]}`

const statusSendError = `{"ok":false,"version":"0.2.3","mcp":{"agent":"claude","url":"http://localhost:8080/mcp"},
"token":{"present":true,"last_sent":"2026-10-01T06:40:00Z","last_error":{"status":500}},"settings":{"cached":true,"version":7,"applied":7},
"queue":{"queued":12},"problems":["последняя отправка не принята сервисом: 500","вторая проблема"]}`

// statusPrototype — что печатает в stderr старый прототип вместо JSON; statusNoMCP — hottell без
// MCP-сервера в конфигах; statusFailed — stderr нового hottell, упавшего до JSON.
const (
	statusPrototype = "использование: hottell -agent claude|codex | mcp"
	statusNoMCP     = `{"ok":false,"mcp":{},"problems":["MCP hottell не найден"]}`
	statusFailed    = "hottell status: $HOME is not defined\nвторая строка"
)

func statusOf(t *testing.T, s *server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// optional — ожидаемое значение поля ответа: пустая строка — поля нет.
func optional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestSendStatusStates(t *testing.T) {
	for name, tc := range map[string]struct {
		bin     func(t *testing.T) string
		state   string
		reason  string
		origin  string
		problem string // status.problems[0]
	}{
		"бинаря нет": {
			bin:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") },
			state: "not_configured", reason: "бинаря hottell нет",
		},
		"подключено": {
			bin:   func(t *testing.T) string { return fakeHottell(t, statusConnected, 0) },
			state: "ok", origin: "http://localhost:8080",
		},
		"отправка с ошибкой": {
			bin:   func(t *testing.T) string { return fakeHottell(t, statusSendError, 1) },
			state: "problem", origin: "http://localhost:8080", problem: "последняя отправка не принята сервисом: 500",
		},
		"старый прототип": {
			bin:   func(t *testing.T) string { return fakeHottellStderr(t, statusPrototype, 2) },
			state: "not_configured", reason: "hottell status --json не дал JSON — бинарь не той версии",
		},
		"нет подключения": {
			bin:   func(t *testing.T) string { return fakeHottell(t, statusNoMCP, 1) },
			state: "not_configured", reason: "MCP hottell не подключён",
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := testServer(t)
			s.hottell = tc.bin(t)
			s.checkSendStatus()
			got := statusOf(t, s)
			if got["state"] != tc.state || got["reason"] != optional(tc.reason) || got["service_origin"] != optional(tc.origin) {
				t.Fatalf("%+v", got)
			}
			if tc.state != "not_configured" && got["status"] == nil {
				t.Fatal("вывод hottell status отдаётся как есть")
			}
			if tc.problem != "" {
				status, _ := got["status"].(map[string]any)
				problems, _ := status["problems"].([]any)
				if len(problems) == 0 || problems[0] != tc.problem {
					t.Fatalf("status.problems: %+v", status)
				}
			}
		})
	}
}

// TestClassifyStatusWithoutJSON: без JSON причина говорит, что случилось, а не только «не та версия».
func TestClassifyStatusWithoutJSON(t *testing.T) {
	run := func(bin string) ([]byte, error) { return hottellStatus(context.Background(), bin) }
	for name, tc := range map[string]struct {
		run    func(t *testing.T) ([]byte, error)
		reason string
	}{
		"тайм-аут": {
			run:    func(t *testing.T) ([]byte, error) { return nil, context.DeadlineExceeded },
			reason: "hottell status не ответил за 20 с",
		},
		"код 1, только stderr": {
			run:    func(t *testing.T) ([]byte, error) { return run(fakeHottellStderr(t, statusFailed, 1)) },
			reason: "hottell status завершился с ошибкой: hottell status: $HOME is not defined",
		},
		"код 1 молча": {
			run:    func(t *testing.T) ([]byte, error) { return run(hottellScript(t, "exit 1\n")) },
			reason: "hottell status завершился с ошибкой: exit status 1",
		},
		"код 1, длинная строка stderr": {
			run: func(t *testing.T) ([]byte, error) {
				return run(fakeHottellStderr(t, strings.Repeat("я", 300)+"\nвторая строка", 1))
			},
			reason: "hottell status завершился с ошибкой: " + strings.Repeat("я", 200) + "…",
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := tc.run(t)
			st := classifyStatus(out, err, time.Now())
			if st.State != "not_configured" || st.Reason != tc.reason || st.Status != nil {
				t.Fatalf("%+v", st)
			}
		})
	}
}

// TestHottellStatusTimeout: срок ограничивает и ожидание детей, которые держат stdout открытым.
func TestHottellStatusTimeout(t *testing.T) {
	bin := hottellScript(t, "[ \"$1\" = status ] || exit 0\nsleep 5 &\nsleep 5\n")
	// Первый запуск нового файла macOS может задержать на секунды: прогрев без аргументов.
	if err := exec.Command(bin).Run(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := hottellStatus(ctx, bin)
	if took := time.Since(start); !errors.Is(err, context.DeadlineExceeded) || took > 3*time.Second {
		t.Fatalf("%v за %s", err, took)
	}
}

func TestSendStatusLoopbackOnly(t *testing.T) {
	s, _ := testServer(t)
	s.hottell = fakeHottell(t, statusConnected, 0)
	s.checkSendStatus()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil) // RemoteAddr 192.0.2.1
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "localhost:8080") {
		t.Fatalf("не loopback: %d %s", rec.Code, rec.Body)
	}
}

func TestSendStatusBeforeFirstCheck(t *testing.T) {
	s, _ := testServer(t)
	if got := statusOf(t, s); got["state"] != "unknown" {
		t.Fatalf("до первой проверки: %+v", got)
	}
}
