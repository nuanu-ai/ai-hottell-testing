package ioc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/cmd/api/ioc"
	"git.alva.dev/alva/harness-telemetry/internal/application/session/mock"
)

const (
	giteaRepo  = "alva/harness-telemetry"
	giteaToken = "test-gitea-token"
	releaseTag = "hottell-v0.1.0"
)

// fakeHottell stands in for the binary: it records the arguments it is run with in the
// file $HOTTELL_TEST_MARK and touches nothing else.
const fakeHottell = "#!/bin/sh\necho \"hottell $*\" >\"$HOTTELL_TEST_MARK\"\n"

// newFakeGitea serves a Gitea with the release hottell-v0.1.0 of giteaRepo holding files,
// both read with giteaToken only.
func newFakeGitea(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	var assets []string
	for name := range files {
		assets = append(assets, `{"name":"`+name+`"}`)
	}
	releases := `[{"tag_name":"` + releaseTag + `","assets":[` + strings.Join(assets, ",") + `]}]`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/repos/"+giteaRepo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token "+giteaToken {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, releases)
	})
	mux.HandleFunc("GET /"+giteaRepo+"/releases/download/"+releaseTag+"/{name}",
		func(w http.ResponseWriter, r *http.Request) {
			file, ok := files[r.PathValue("name")]
			if r.Header.Get("Authorization") != "token "+giteaToken || !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, file)
		})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// releaseFiles returns the four files of a release as HT-102 publishes them: the real
// install.sh, both binaries as fakeHottell and their SHA256SUMS.
func releaseFiles(t *testing.T) map[string]string {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "deployments", "hottell", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	sum := sha256.Sum256([]byte(fakeHottell))
	digest := hex.EncodeToString(sum[:])
	return map[string]string{
		"install.sh":           string(script),
		"hottell-darwin-arm64": fakeHottell,
		"hottell-darwin-amd64": fakeHottell,
		"SHA256SUMS":           digest + "  hottell-darwin-arm64\n" + digest + "  hottell-darwin-amd64\n",
	}
}

// newDownloadService returns the assembled service at origin, taking the releases from
// the Gitea at giteaURL.
func newDownloadService(t *testing.T, giteaURL string) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	cfg := testConfig()
	cfg.PublicOrigin = "http://" + srv.Listener.Addr().String()
	cfg.GiteaURL, cfg.GiteaRepo, cfg.GiteaToken = giteaURL, giteaRepo, giteaToken
	container, err := ioc.New(pingerFunc(func(context.Context) error { return nil }),
		stores(t, mock.NewMockSessions(gomock.NewController(t))), cfg, "dev", slog.New(slog.DiscardHandler),
		fstest.MapFS{"index.html": {Data: []byte(indexHTML)}})
	if err != nil {
		t.Fatalf("assemble container: %v", err)
	}
	srv.Config.Handler = container.Handler
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}

func TestDownloadRoutes(t *testing.T) {
	t.Parallel()
	files := releaseFiles(t)
	service := newDownloadService(t, newFakeGitea(t, files).URL)

	t.Run("install.sh carries the public origin", func(t *testing.T) {
		t.Parallel()
		code, body := get(t, service.URL+"/install.sh")
		want := strings.ReplaceAll(files["install.sh"], "__ORIGIN__", service.URL)
		if code != http.StatusOK || body != want || strings.Contains(body, "__ORIGIN__") {
			t.Fatalf("got %d, body with the origin put in: %t", code, body == want)
		}
	})
	for _, name := range []string{"hottell-darwin-arm64", "hottell-darwin-amd64", "SHA256SUMS"} {
		t.Run("download "+name, func(t *testing.T) {
			t.Parallel()
			if code, body := get(t, service.URL+"/download/"+name); code != http.StatusOK || body != files[name] {
				t.Fatalf("got %d %q, want 200 %q", code, body, files[name])
			}
		})
	}
	for _, name := range []string{"install.sh", "hottell", "nested/SHA256SUMS"} {
		t.Run("unknown "+name, func(t *testing.T) {
			t.Parallel()
			if code, body := get(t, service.URL+"/download/"+name); code != http.StatusNotFound {
				t.Fatalf("got %d %q, want 404", code, body)
			}
		})
	}
}

func TestDownloadGiteaUnavailable(t *testing.T) {
	t.Parallel()
	gitea := newFakeGitea(t, releaseFiles(t))
	gitea.Close()
	service := newDownloadService(t, gitea.URL)

	for _, path := range []string{"/install.sh", "/download/hottell-darwin-arm64", "/download/SHA256SUMS"} {
		code, body := get(t, service.URL+path)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "unavailable") {
			t.Fatalf("%s: got %d %q, want 503 with a text", path, code, body)
		}
	}
}

// TestInstallScriptInstallsTheReleaseBinary runs `curl <service>/install.sh | sh` against
// the service and a fake Gitea: the script downloads the binary for this Mac, checks it
// against SHA256SUMS and runs `hottell install`, here the fake that only records it ran.
func TestInstallScriptInstallsTheReleaseBinary(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("install.sh runs on macOS only")
	}
	service := newDownloadService(t, newFakeGitea(t, releaseFiles(t)).URL)
	mark := filepath.Join(t.TempDir(), "ran")

	cmd := exec.CommandContext(t.Context(), "sh", "-c", `curl -fsSL "$SERVICE/install.sh" | sh`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
		"NO_PROXY=*",
		"SERVICE=" + service.URL,
		"HOTTELL_TEST_MARK=" + mark,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("curl | sh: %v\n%s", err, out)
	}
	ran, err := os.ReadFile(mark)
	if err != nil || string(ran) != "hottell install\n" {
		t.Fatalf("the downloaded binary ran as %q (%v), want \"hottell install\"\n%s", ran, err, out)
	}
}
