package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const sidLive = "01a0f57b-c10a-7243-9e0f-a1b774b94b74"

// fakeClickHouse отвечает на два запроса пульса и считает обращения.
func fakeClickHouse(t *testing.T, calls *int32) *httptest.Server {
	return fakeClickHouseActive(t, calls, `{"sid":"`+sidLive+`","agent":"codex","cwd":"/Users/x/Life/projects/ai-hottell","last_at":"2026-10-01 06:58:25.000000000","per_min":12}`)
}

func fakeClickHouseActive(t *testing.T, calls *int32, active string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		b, _ := io.ReadAll(r.Body)
		switch q := string(b); {
		case strings.Contains(q, "INTERVAL 10 SECOND"):
			io.WriteString(w, `{"t":"2026-10-01 06:58:10.000000000","agent":"codex","n":7}`+"\n"+`{"t":"2026-10-01 06:58:20.000000000","agent":"claude","n":3}`+"\n")
		case strings.Contains(q, "per_min"):
			io.WriteString(w, active+"\n")
		default:
			http.Error(w, "unexpected query", 400)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pulseOf(t *testing.T, s *server, remote string) (int, pulse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pulse", nil)
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:8800" // как страница дашборда: Host — адрес -listen
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var p pulse
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return rec.Code, p
}

func TestPulse(t *testing.T) {
	var calls int32
	s, dir := testServer(t)
	s.pulse.clickhouse, s.pulse.db = fakeClickHouse(t, &calls).URL, "otel"
	if err := os.WriteFile(filepath.Join(dir, "dataset.json"),
		[]byte(`{"sessions":[{"id":"`+sidLive+`","project":"ai-hottell","first":"Почини сборку"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, p := pulseOf(t, s, "127.0.0.1:5000")
	if code != 200 || len(p.Bars) != 2 || p.Bars[0].T != "2026-10-01T06:58:10Z" || p.Bars[0].N != 7 {
		t.Fatalf("bars: %d %+v", code, p)
	}
	if len(p.Active) != 1 || p.Active[0].First != "Почини сборку" || p.Active[0].Project != "ai-hottell" || p.Active[0].PerMin != 12 {
		t.Fatalf("active — первая реплика и проект из датасета: %+v", p.Active)
	}
	if p.Active[0].LastAt != "2026-10-01T06:58:25Z" {
		t.Fatalf("время — RFC3339 UTC: %q", p.Active[0].LastAt)
	}
	pulseOf(t, s, "127.0.0.1:5000")
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("повтор в пределах 5 с берётся из кэша: обращений к ClickHouse %d", n)
	}
	if code, _ := pulseOf(t, s, "192.0.2.1:5000"); code != http.StatusForbidden {
		t.Fatalf("не loopback: %d", code)
	}
}

func TestPulseUnavailable(t *testing.T) {
	s, _ := testServer(t)
	if code, _ := pulseOf(t, s, "127.0.0.1:5000"); code != http.StatusNotFound {
		t.Fatalf("без -clickhouse пульса нет (:8800): %d", code)
	}
	s.pulse.clickhouse = "http://127.0.0.1:1" // никто не слушает
	code, p := pulseOf(t, s, "127.0.0.1:5000")
	if code != 200 || p.Error == "" || p.Bars == nil || len(p.Bars) != 0 {
		t.Fatalf("ClickHouse недоступен — ошибка в ответе, пустые ряды: %d %+v", code, p)
	}
}

func TestMetaPulse(t *testing.T) {
	s, _ := testServer(t)
	if rec := get(t, s.routes(), "/api/meta"); !strings.Contains(rec.Body.String(), `"pulse":false`) {
		t.Fatalf("без -clickhouse: %s", rec.Body)
	}
	s.pulse.clickhouse = "http://127.0.0.1:8123"
	if rec := get(t, s.routes(), "/api/meta"); !strings.Contains(rec.Body.String(), `"pulse":true`) {
		t.Fatalf("с -clickhouse: %s", rec.Body)
	}
}

func TestPulseUnknownSession(t *testing.T) {
	var calls int32
	s, _ := testServer(t)
	s.pulse.clickhouse = fakeClickHouseActive(t, &calls, `{"sid":"01a0f57b-0000-7000-8000-000000000001","agent":"claude","cwd":"","last_at":"2026-10-01 06:58:25.000000000","per_min":3}`).URL
	_, p := pulseOf(t, s, "127.0.0.1:5000")
	if len(p.Active) != 1 || p.Active[0].Project != "" || p.Active[0].First != "" {
		t.Fatalf("сессии нет в датасете и cwd пуст — проект пустой, не «.»: %+v", p.Active)
	}
}

func TestPulseClickHouseError(t *testing.T) {
	s, _ := testServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Code: 60. DB::Exception: Table otel.otel_logs does not exist", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	s.pulse.clickhouse = srv.URL
	code, p := pulseOf(t, s, "127.0.0.1:5000")
	if code != 200 || !strings.Contains(p.Error, "otel_logs does not exist") || len(p.Bars) != 0 || len(p.Active) != 0 {
		t.Fatalf("ошибка ClickHouse — в ответе, ряды пустые: %d %+v", code, p)
	}
}
