package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "tok-SECRET-7f3a"

// stubTokenSource — источник токена для тестов пакета (setup и др.): отдаёт
// заданный ответ и считает вызовы, чтобы проверять «диалог не больше одного раза».
type stubTokenSource struct {
	token   string
	outcome acquireOutcome
	calls   int
}

func (s *stubTokenSource) Ask(context.Context) (string, acquireOutcome) {
	s.calls++
	return s.token, s.outcome
}

// collector — httptest-сервер приёма: отвечает заданным статусом и телом,
// запоминает последний запрос.
type collectorStub struct {
	srv      *httptest.Server
	hits     atomic.Int32
	mu       sync.Mutex // запрос пишется в горутине сервера
	method   string
	path     string
	auth     string
	body     string
	respCode int
	respBody string
	location string
}

func newCollector(t *testing.T, code int, body string) *collectorStub {
	t.Helper()
	c := &collectorStub{respCode: code, respBody: body}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.method, c.path, c.auth, c.body = r.Method, r.URL.Path, r.Header.Get("Authorization"), string(b)
		if c.location != "" {
			w.Header().Set("Location", c.location)
		}
		w.WriteHeader(c.respCode)
		_, _ = io.WriteString(w, c.respBody)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func TestValidateToken(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		body   string
		want   tokenStatus
		detail string // подстрока, которая должна быть в Detail
	}{
		{"200 empty body", 200, "", tokenAccepted, ""},
		{"200 empty partialSuccess", 200, `{"partialSuccess":{}}`, tokenAccepted, ""},
		{"200 zero rejected", 200, `{"partialSuccess":{"rejectedLogRecords":"0"}}`, tokenAccepted, ""},
		// protobuf-JSON кодирует int64 строкой; число тоже встречается.
		{"200 partial rejection string", 200, `{"partialSuccess":{"rejectedLogRecords":"1","errorMessage":"bad"}}`, tokenUnknown, "rejected"},
		{"200 partial rejection number", 200, `{"partialSuccess":{"rejectedLogRecords":2}}`, tokenUnknown, "rejected"},
		{"401", 401, "", tokenRejected, "401"},
		{"403", 403, "", tokenRejected, "403"},
		{"500 echoing the header", 500, "auth was Bearer " + testToken, tokenUnknown, "500"},
		{"404", 404, "", tokenUnknown, "404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCollector(t, tc.code, tc.body)
			url := c.srv.URL + "/v1/logs"
			got := validateToken(context.Background(), url, testToken)
			if got.Status != tc.want {
				t.Fatalf("status = %v, want %v (detail %q)", got.Status, tc.want, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.detail) {
				t.Errorf("detail %q lacks %q", got.Detail, tc.detail)
			}
			if tc.want != tokenAccepted && !strings.Contains(got.Detail, url) {
				t.Errorf("detail %q does not name the endpoint", got.Detail)
			}
			if strings.Contains(got.Detail, testToken) {
				t.Errorf("detail leaks the token: %q", got.Detail)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.method != http.MethodPost || c.path != "/v1/logs" || c.auth != "Bearer "+testToken || c.body != `{"resourceLogs":[]}` {
				t.Errorf("request = %s %s auth=%q body=%q", c.method, c.path, c.auth, c.body)
			}
		})
	}
}

// Редирект не принимается за приём и не уносит токен на другой адрес.
func TestValidateTokenRefusesRedirect(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		elsewhere := newCollector(t, 200, "")
		c := newCollector(t, code, "")
		c.location = elsewhere.srv.URL + "/v1/logs"
		got := validateToken(context.Background(), c.srv.URL+"/v1/logs", testToken)
		if got.Status != tokenUnknown {
			t.Errorf("%d: status = %v, want unknown", code, got.Status)
		}
		if n := elsewhere.hits.Load(); n != 0 {
			t.Errorf("%d: redirect followed, target hit %d times", code, n)
		}
	}
}

func TestValidateTokenNetworkError(t *testing.T) {
	c := newCollector(t, 200, "")
	url := c.srv.URL + "/v1/logs?k=" + testToken // токен в тексте ошибки транспорта
	c.srv.Close()
	got := validateToken(context.Background(), url, testToken)
	if got.Status != tokenUnknown {
		t.Fatalf("status = %v, want unknown", got.Status)
	}
	if got.Detail == "" || strings.Contains(got.Detail, testToken) {
		t.Errorf("detail = %q: want non-empty and scrubbed", got.Detail)
	}
}

// fakeOsascript пишет исполняемый shell-скрипт вместо /usr/bin/osascript.
func fakeOsascript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "osascript")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDialogSource(t *testing.T) {
	long := strings.Repeat("a", 4097)
	cases := []struct {
		name  string
		fake  string
		want  acquireOutcome
		token string
	}{
		{"typed token", `printf '%s\n' ` + testToken, acquireAccepted, testToken},
		{"cancel", `echo "0:1: execution error: User canceled. (-128)" >&2; exit 1`, acquireCancelled, ""},
		{"gave up", `echo "40:65: execution error: hottell: dialog gave up (1)" >&2; exit 1`, acquireTimeout, ""},
		{"no GUI", `echo "execution error: No user interaction allowed. (-1713)" >&2; exit 1`, acquireUnavailable, ""},
		{"empty answer", `echo ""`, acquireInvalid, ""},
		{"too long answer", `echo ` + long, acquireInvalid, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := dialogSource{osascript: fakeOsascript(t, tc.fake)}
			tok, got := d.Ask(context.Background())
			if got != tc.want || tok != tc.token {
				t.Fatalf("Ask = (%q, %v), want (%q, %v)", tok, got, tc.token, tc.want)
			}
		})
	}
}

func TestDialogSourceMissingBinary(t *testing.T) {
	d := dialogSource{osascript: filepath.Join(t.TempDir(), "no-such-osascript")}
	if tok, got := d.Ask(context.Background()); got != acquireUnavailable || tok != "" {
		t.Fatalf("Ask = (%q, %v), want unavailable", tok, got)
	}
}

// Зависший диалог обрывается контекстом и считается таймаутом, а не «нет GUI».
func TestDialogSourceContextDeadline(t *testing.T) {
	d := dialogSource{osascript: fakeOsascript(t, `sleep 5; echo late`)}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	tok, got := d.Ask(ctx)
	if got != acquireTimeout || tok != "" {
		t.Fatalf("Ask = (%q, %v), want timeout", tok, got)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("Ask did not return at the deadline")
	}
}

// Настоящий osascript исполняет обработчик ответа диалога: «время вышло»
// проверяется до чтения текста, и при заполненном, и при пустом поле.
func TestDialogHandlerGaveUpBeforeText(t *testing.T) {
	if _, err := os.Stat("/usr/bin/osascript"); err != nil {
		t.Skip("no /usr/bin/osascript")
	}
	cases := []struct {
		record  string
		wantOut string
		wantErr bool
	}{
		{`{text returned:"half-typed", gave up:true}`, "", true},
		{`{text returned:"", gave up:true}`, "", true},
		{`{text returned:"abc", gave up:false}`, "abc", false},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command("/usr/bin/osascript", "-e", dialogHandler, "-e", "answer("+tc.record+")")
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err = %v, stderr %q", tc.record, err, stderr.String())
		}
		if tc.wantErr && !strings.Contains(stderr.String(), dialogGaveUpMarker) {
			t.Errorf("%s: stderr %q lacks %q", tc.record, stderr.String(), dialogGaveUpMarker)
		}
		if strings.TrimSpace(stdout.String()) != tc.wantOut {
			t.Errorf("%s: stdout %q, want %q", tc.record, stdout.String(), tc.wantOut)
		}
	}
}

func TestWriteSecretFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "sub", "new.toml")
	for _, p := range []string{existing, fresh} {
		if err := writeSecretFile(p, []byte("new")); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %o, want 600", p, st.Mode().Perm())
		}
		if b, _ := os.ReadFile(p); string(b) != "new" {
			t.Errorf("%s: content %q", p, b)
		}
	}
	for _, d := range []string{dir, filepath.Join(dir, "sub")} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if e.Name() != "settings.json" && e.Name() != "sub" && e.Name() != "new.toml" {
				t.Errorf("leftover temp file %s in %s", e.Name(), d)
			}
		}
	}
}

func TestSaveTokenTightensPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hottell")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveToken(path, testToken); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %o, want 700", st.Mode().Perm())
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o, want 600", st.Mode().Perm())
	}
	if (Config{TokenFile: path}).token() != testToken {
		t.Errorf("saved token not read back")
	}
}

// stdinFile — не-терминальный stdin с заданным содержимым.
func stdinFile(t *testing.T, content string) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestTokenCommand(t *testing.T) {
	cases := []struct {
		name      string
		stdin     string
		code      int
		wantExit  int
		wantSaved bool
		wantHits  int32
	}{
		{"accepted", testToken + "\n", 200, 0, true, 1},
		{"rejected keeps old", testToken + "\n", 401, 1, false, 1},
		{"unknown keeps old", testToken + "\n", 503, 1, false, 1},
		{"empty line", "\n", 200, 1, false, 0},
		{"too long", strings.Repeat("x", 4097) + "\n", 200, 1, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCollector(t, tc.code, "")
			tokPath := filepath.Join(t.TempDir(), "hottell", "token")
			if err := os.MkdirAll(filepath.Dir(tokPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tokPath, []byte("previous\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := Config{Endpoint: c.srv.URL + "/v1/logs", TokenFile: tokPath}
			var out, errOut bytes.Buffer
			exit := runTokenCmd(context.Background(), stdinFile(t, tc.stdin), &out, &errOut, cfg)
			if exit != tc.wantExit {
				t.Errorf("exit = %d, want %d (out %q, err %q)", exit, tc.wantExit, out.String(), errOut.String())
			}
			if n := c.hits.Load(); n != tc.wantHits {
				t.Errorf("collector hits = %d, want %d", n, tc.wantHits)
			}
			want := "previous"
			if tc.wantSaved {
				want = testToken
			}
			if got := cfg.token(); got != want {
				t.Errorf("token file = %q, want %q", got, want)
			}
			if strings.Contains(out.String()+errOut.String(), testToken) {
				t.Errorf("output leaks the token: %q / %q", out.String(), errOut.String())
			}
			if out.Len() == 0 && errOut.Len() == 0 {
				t.Errorf("no outcome printed")
			}
		})
	}
}
