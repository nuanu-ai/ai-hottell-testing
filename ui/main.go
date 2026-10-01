// Command ai-hottell-ui serves the hottell v3 dashboard: one static page and the
// dataset produced by builder/build.py. It reads only local files and listens on
// the loopback interface; the page itself never invents values.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed static
var static embed.FS

var sessionID = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

type server struct {
	dataDir string
	journal string   // журнал hottell-coach; по умолчанию <родитель dataDir>/coach/journal.jsonl
	page    fs.FS    // static page; embedded unless -static is set
	builder []string // command that rebuilds dataDir; empty disables rebuild
	name    string   // version label shown in the header
	links   links    // other dashboard versions, shown as a switch in the header
	refresh time.Duration
	pulse   pulseSource
	buildMu sync.Mutex // one builder run at a time; readers need no lock, builders write files atomically
	hottell string     // hottell for the send status; empty disables it
	status  statusBox  // last hottell status --json, served at /api/status
}

type link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// links is a repeatable -link "label=url" flag.
type links []link

func (l *links) String() string { return fmt.Sprint(*l) }

func (l *links) Set(v string) error {
	label, u, ok := strings.Cut(v, "=")
	if !ok || label == "" || u == "" {
		return errors.New(`want -link "label=url"`)
	}
	*l = append(*l, link{Label: label, URL: u})
	return nil
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8800", "address (loopback only)")
	dataDir := flag.String("data", "../local-data/ui", "directory with dataset.json and sessions/")
	builder := flag.String("builder", "builder/build.py", "path to build.py; empty disables «Пересобрать»")
	clickhouse := flag.String("clickhouse", "", "ClickHouse HTTP for the header pulse (empty: no pulse, as :8800)")
	chDB := flag.String("clickhouse-db", "otel", "ClickHouse database for the pulse")
	staticDir := flag.String("static", "", "serve the page from this directory instead of the embedded copy (development)")
	name := flag.String("name", "", "version label shown in the header")
	refresh := flag.Duration("refresh", 0, "rebuild the dataset on this interval (0 = only by the button)")
	journal := flag.String("journal", "", "журнал hottell-coach (по умолчанию <родитель -data>/coach/journal.jsonl)")
	defaultHottell := "" // no home directory: the send status check is off unless -hottell is set
	if home, err := os.UserHomeDir(); err == nil {
		defaultHottell = filepath.Join(home, ".local", "bin", "hottell")
	}
	hottell := flag.String("hottell", defaultHottell, "hottell binary for the send status (empty disables it)")
	var other links
	flag.Var(&other, "link", `another dashboard version for the header switch, "label=url" (repeatable)`)
	flag.Parse()

	if err := checkLoopback(*listen); err != nil {
		log.Fatal(err)
	}
	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	s := &server{dataDir: abs, name: *name, links: other, refresh: *refresh, hottell: *hottell}
	s.journal = *journal
	if s.journal == "" {
		s.journal = filepath.Join(filepath.Dir(abs), "coach", "journal.jsonl") // local-data/ui и local-data/ui-live → local-data/coach
	}
	if *staticDir != "" {
		s.page = os.DirFS(*staticDir)
	}
	s.pulse.clickhouse, s.pulse.db = strings.TrimRight(*clickhouse, "/"), *chDB
	if *builder != "" {
		b, err := filepath.Abs(*builder)
		if err != nil {
			log.Fatal(err)
		}
		s.builder = []string{"python3", b, "--out", abs}
	}
	if s.refresh > 0 && len(s.builder) > 0 {
		go s.rebuildEvery(s.refresh)
	}
	if s.hottell != "" {
		go s.watchSendStatus(time.Minute)
	}
	log.Printf("hottell ui: http://%s  data=%s", *listen, abs)
	log.Fatal(http.ListenAndServe(*listen, s.routes()))
}

func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("listen: only loopback addresses are allowed, got " + addr)
}

func (s *server) routes() http.Handler {
	page := s.page
	if page == nil {
		sub, err := fs.Sub(static, "static")
		if err != nil {
			panic(err)
		}
		page = sub
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(page))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/dataset", s.dataset)
	mux.HandleFunc("GET /api/pulse", s.pulseHandler)
	mux.HandleFunc("GET /api/sessions/{id}", s.session)
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("GET /api/coach/journal", s.coachJournal)
	mux.HandleFunc("POST /api/rebuild", s.rebuild)
	mux.HandleFunc("GET /api/status", s.sendStatusHandler)
	return mux
}

func (s *server) dataset(w http.ResponseWriter, r *http.Request) {
	s.serveJSONFile(w, filepath.Join(s.dataDir, "dataset.json"),
		"Датасет ещё не собран: запустите python3 ui/builder/build.py")
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !sessionID.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный id сессии"})
		return
	}
	s.serveJSONFile(w, filepath.Join(s.dataDir, "sessions", id+".json"), "Ленты этой сессии нет в датасете")
}

func (s *server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"rebuild": len(s.builder) > 0, "name": s.name, "links": s.links, "refresh_s": int(s.refresh.Seconds()),
		"pulse": s.pulse.clickhouse != "",
	})
}

func (s *server) serveJSONFile(w http.ResponseWriter, path, missing string) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": missing})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

// rebuild runs the builder with fixed arguments. Requests from other origins are
// refused so a web page elsewhere cannot trigger it through the browser.
func (s *server) rebuild(w http.ResponseWriter, r *http.Request) {
	if len(s.builder) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "пересборка отключена"})
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "запрос не с этой страницы"})
		return
	}
	tail, err := s.runBuilder(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "output": tail})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": tail})
}

// runBuilder runs the builder once.
func (s *server) runBuilder(parent context.Context) (string, error) {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, s.builder[0], s.builder[1:]...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	tail := out.String()
	if len(tail) > 4000 {
		tail = tail[len(tail)-4000:]
	}
	return tail, err
}

func (s *server) rebuildEvery(d time.Duration) {
	for range time.Tick(d) {
		if tail, err := s.runBuilder(context.Background()); err != nil {
			log.Printf("rebuild: %v\n%s", err, tail)
		}
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
