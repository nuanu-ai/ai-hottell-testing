package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testID = "00000000-0000-4000-8000-00000000000a"

func testServer(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "dataset.json"), []byte(`{"schema_version":1}`), 0o600)
	os.WriteFile(filepath.Join(dir, "sessions", testID+".json"), []byte(`{"id":"`+testID+`"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "secret.json"), []byte(`{"secret":1}`), 0o600)
	return &server{dataDir: dir}, dir
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestPageAndDataset(t *testing.T) {
	s, _ := testServer(t)
	h := s.routes()
	if rec := get(t, h, "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "hottell") {
		t.Fatalf("page: %d", rec.Code)
	}
	rec := get(t, h, "/api/dataset")
	if rec.Code != 200 || rec.Body.String() != `{"schema_version":1}` {
		t.Fatalf("dataset: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("dataset must not be cached")
	}
}

func TestSessionIDValidation(t *testing.T) {
	s, _ := testServer(t)
	h := s.routes()
	if rec := get(t, h, "/api/sessions/"+testID); rec.Code != 200 {
		t.Fatalf("valid id: %d", rec.Code)
	}
	for _, bad := range []string{"..%2Fsecret", "secret", "00000000", testID + "x"} {
		if rec := get(t, h, "/api/sessions/"+bad); rec.Code != 400 && rec.Code != 404 {
			t.Fatalf("%s: got %d", bad, rec.Code)
		} else if strings.Contains(rec.Body.String(), "secret") && rec.Code == 200 {
			t.Fatalf("%s leaked a file", bad)
		}
	}
	if rec := get(t, h, "/api/sessions/00000000-0000-4000-8000-00000000000b"); rec.Code != 404 {
		t.Fatalf("missing session: %d", rec.Code)
	}
}

func TestMissingDataset(t *testing.T) {
	s := &server{dataDir: t.TempDir()}
	rec := get(t, s.routes(), "/api/dataset")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "build.py") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestRebuildRequiresSameOrigin(t *testing.T) {
	s, dir := testServer(t)
	s.builder = []string{"sh", "-c", "echo built > " + filepath.Join(dir, "marker")}
	h := s.routes()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8800/api/rebuild", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker")); err == nil {
		t.Fatal("builder ran for a cross-origin request")
	}

	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8800/api/rebuild", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8800")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("same-origin: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker")); err != nil {
		t.Fatal("builder did not run")
	}
}

func TestLoopbackOnly(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:8800": true, "localhost:8800": true, "[::1]:8800": true, "0.0.0.0:8800": false, ":8800": false} {
		if err := checkLoopback(addr); (err == nil) != ok {
			t.Errorf("%s: err=%v", addr, err)
		}
	}
}

func TestMetaAndLinks(t *testing.T) {
	var l links
	if err := l.Set("Разобранные сессии=http://127.0.0.1:8800"); err != nil {
		t.Fatal(err)
	}
	if err := l.Set("без ссылки"); err == nil {
		t.Fatal("want error for a link without url")
	}
	s, _ := testServer(t)
	s.name, s.links = "Новые сессии", l
	rec := get(t, s.routes(), "/api/meta")
	body := rec.Body.String()
	for _, want := range []string{`"name":"Новые сессии"`, `"url":"http://127.0.0.1:8800"`, `"rebuild":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("meta %s: missing %s", body, want)
		}
	}
}
