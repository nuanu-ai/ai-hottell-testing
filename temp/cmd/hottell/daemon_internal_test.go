package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient/mcptest"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const (
	mcpKey      = "ht_mcp_daemon_fixture"
	firstToken  = "ht_col_daemon_fixture_1"
	secondToken = "ht_col_daemon_fixture_2"
	// daemonWait bounds every wait for the daemon; the sender looks at the queue every
	// 2 s.
	daemonWait = 20 * time.Second
)

// markers finds the markers the test events and lines carry in a request's bytes.
var markers = regexp.MustCompile(`mark-[a-z0-9]+`) //nolint:gochecknoglobals // compiled once

// intake is a test OTLP intake that answers by answer and records the markers and the
// token of every request; while hold is set, a request waits for release.
type intake struct {
	*httptest.Server

	answer func(token string) int

	mu       sync.Mutex
	requests []intakeRequest
	hold     chan struct{}
	arrived  chan struct{}
}

type intakeRequest struct {
	token   string
	markers []string
	body    string
}

func newIntake(t *testing.T, answer func(token string) int) *intake {
	t.Helper()
	in := &intake{answer: answer, arrived: make(chan struct{}, 16)}
	in.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "not gzip", http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(zr)
		if err != nil || r.URL.Path != "/v1/logs" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		in.mu.Lock()
		in.requests = append(in.requests, intakeRequest{token: token, markers: markers.FindAllString(string(raw), -1), body: string(raw)})
		hold := in.hold
		in.mu.Unlock()
		select {
		case in.arrived <- struct{}{}:
		default:
		}
		if hold != nil {
			<-hold
		}
		w.WriteHeader(in.answer(token))
	}))
	t.Cleanup(in.Close)
	return in
}

// sent reports whether a request with token carried marker; any token when token is "".
func (in *intake) sent(token, marker string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, r := range in.requests {
		if (token == "" || r.token == token) && slices.Contains(r.markers, marker) {
			return true
		}
	}
	return false
}

// took reports whether a request with token that the intake answered 200 carried marker.
func (in *intake) took(token, marker string) bool {
	return in.answer(token) == http.StatusOK && in.sent(token, marker)
}

func acceptAll(string) int { return http.StatusOK }

// daemonFixture is a temporary home with Claude Code installed and the hottell MCP server
// in its config, the test MCP server and the test intake.
type daemonFixture struct {
	home   string
	env    map[string]string
	cfg    daemonConfig
	mcp    *mcptest.Server
	intake *intake

	cancel context.CancelFunc
	done   chan error
}

func newDaemonFixture(t *testing.T, answer func(token string) int) *daemonFixture {
	t.Helper()

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &daemonFixture{home: home, intake: newIntake(t, answer)}
	f.mcp = mcptest.New(mcpKey, firstToken, f.intake.URL, `{"version":1}`)
	t.Cleanup(f.mcp.Close)
	writeJSON(t, filepath.Join(home, ".claude.json"), map[string]any{"mcpServers": map[string]any{"hottell": map[string]any{
		"type": "http", "url": f.mcp.URL, "headers": map[string]string{"Authorization": "Bearer " + mcpKey},
	}}})

	f.env = map[string]string{"HOME": home, state.HomeEnv: filepath.Join(home, "state")}
	cfg, err := daemonConfigFrom(lookup(f.env))
	if err != nil {
		t.Fatal(err)
	}
	// Only a signal scans: the tests show that the stop events trigger it.
	cfg.scanEvery = time.Hour
	cfg.tokenEvery = 50 * time.Millisecond
	cfg.grace = daemonWait
	f.cfg = cfg
	return f
}

// start runs the daemon and waits until it has the settings and the first scan is done.
func (f *daemonFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	f.cancel, f.done = cancel, make(chan error, 1)
	go func() { f.done <- serve(ctx, f.cfg) }()
	t.Cleanup(func() {
		cancel()
		<-f.done
	})
	eventually(t, "the settings are cached and the transcripts baselined", func() bool {
		cache, err := f.cfg.paths.ReadSettings()
		_, statErr := os.Stat(filepath.Join(f.cfg.paths.OffsetsDir(), "claude-baseline"))
		return err == nil && cache.Version == 1 && statErr == nil
	})
}

// stop cancels the daemon and returns what serve returned.
func (f *daemonFixture) stop(t *testing.T) error {
	t.Helper()
	f.cancel()
	select {
	case err := <-f.done:
		f.done <- err // for the cleanup
		return err
	case <-time.After(daemonWait):
		t.Fatal("the daemon did not stop")
		return nil
	}
}

// hook runs hottell hook claude with a Claude Code event carrying marker.
func (f *daemonFixture) hook(t *testing.T, name, marker string) {
	t.Helper()
	event := `{"session_id":"s1","hook_event_name":"` + name + `","cwd":"` + f.home + `","m":"` + marker + `"}`
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "claude"}, strings.NewReader(event), &stdout, &stderr, lookup(f.env)); code != exitOK {
		t.Fatalf("hook exit code %d", code)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.paths.Logs, hookLog)); err == nil {
		data, _ := os.ReadFile(filepath.Join(f.cfg.paths.Logs, hookLog))
		t.Fatalf("hook log: %s", data)
	}
}

// claudeEnv is the env section of the Claude Code settings.
func (f *daemonFixture) claudeEnv(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, ".claude", "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	return settings.Env
}

func (f *daemonFixture) queued(t *testing.T) int {
	t.Helper()
	st, err := queue.Open(f.cfg.paths, 0, nil).Stats()
	if err != nil {
		t.Fatal(err)
	}
	return st.Queued
}

func TestDaemon(t *testing.T) {
	t.Parallel()

	f := newDaemonFixture(t, acceptAll)
	transcriptPath := filepath.Join(f.home, ".claude", "projects", "p", "s1.jsonl")
	line := func(marker string) string {
		return `{"type":"user","sessionId":"s1","cwd":"` + f.home + `","m":"` + marker + `"}` + "\n"
	}
	if err := os.WriteFile(transcriptPath, []byte(line("mark-history")), 0o600); err != nil {
		t.Fatal(err)
	}
	f.start(t)

	eventually(t, "the settings are applied to Claude Code", func() bool {
		env := f.claudeEnv(t)
		return env["OTEL_EXPORTER_OTLP_HEADERS"] == "Authorization=Bearer "+firstToken && env["OTEL_LOGS_EXPORTER"] == "otlp"
	})

	// A line appended after the first scan goes out once the agent stops; the history
	// stays, since the settings do not ask for it.
	fh, err := os.OpenFile(transcriptPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(line("mark-live")); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	f.hook(t, "Stop", "mark-hook")
	eventually(t, "the hook event and the transcript line reach the intake", func() bool {
		return f.intake.took(firstToken, "mark-hook") && f.intake.took(firstToken, "mark-live")
	})
	if f.intake.sent("", "mark-history") {
		t.Error("the history was sent without backfill_history")
	}

	if err := f.mcp.SetSettings(t.Context(), `{"version":2,"agents":{"claude":{"sources":{"native_logs":false}}}}`); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new settings version turns the Claude Code logs off", func() bool {
		return f.claudeEnv(t)["OTEL_LOGS_EXPORTER"] == "none"
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"daemon"}, nil, &stdout, &stderr, lookup(f.env)); code != exitFailure {
		t.Errorf("second daemon exit code %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), errAlreadyRunning.Error()) {
		t.Errorf("second daemon stderr = %q, want %q", stderr.String(), errAlreadyRunning)
	}

	if err := f.stop(t); err != nil {
		t.Fatalf("serve: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(f.cfg.paths.Logs, daemonLog))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"the daemon starts", "settings applied", "the daemon stopped"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("daemon log lacks %q:\n%s", want, log)
		}
	}
}

func TestDaemonFetchesTheTokenAgainOn401(t *testing.T) {
	t.Parallel()

	f := newDaemonFixture(t, func(token string) int {
		if token == secondToken {
			return http.StatusOK
		}
		return http.StatusUnauthorized
	})
	f.start(t)
	f.hook(t, "PostToolUse", "mark-event")
	eventually(t, "the service refuses the first token", func() bool {
		return f.intake.sent(firstToken, "mark-event")
	})
	// The token is reissued in the interface only after the refusal: the daemon keeps
	// fetching until it gets another one.
	time.Sleep(200 * time.Millisecond)
	f.mcp.SetToken(secondToken)

	eventually(t, "the event goes out with the reissued token", func() bool {
		return f.intake.took(secondToken, "mark-event")
	})
	eventually(t, "the reissued token is applied to Claude Code", func() bool {
		return f.claudeEnv(t)["OTEL_EXPORTER_OTLP_HEADERS"] == "Authorization=Bearer "+secondToken
	})
}

func TestDaemonStopWaitsForTheSend(t *testing.T) {
	t.Parallel()

	f := newDaemonFixture(t, acceptAll)
	release := make(chan struct{})
	f.intake.mu.Lock()
	f.intake.hold = release
	f.intake.mu.Unlock()
	f.start(t)

	f.hook(t, "PostToolUse", "mark-event")
	select {
	case <-f.intake.arrived:
	case <-time.After(daemonWait):
		t.Fatal("no request reached the intake")
	}
	f.cancel()
	select {
	case err := <-f.done:
		f.done <- err
		close(release)
		t.Fatal("the daemon stopped before the send in flight was answered")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := f.stop(t); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if n := f.queued(t); n != 0 {
		t.Errorf("%d records queued after the stop, want the one in flight acked", n)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// eventually polls cond until it holds or daemonWait passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(daemonWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
