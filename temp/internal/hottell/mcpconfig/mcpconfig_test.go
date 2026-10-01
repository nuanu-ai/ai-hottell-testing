package mcpconfig_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
)

const (
	claudeURL = "http://localhost:18080/mcp"
	claudeKey = "Bearer ht_mcp_claude_fixture"
	codexURL  = "http://localhost:18081/mcp"
	codexKey  = "Bearer ht_mcp_codex_fixture"
)

// sources returns the Claude Code and Codex configs of a fixture directory, in the
// order Find reads them.
func sources(dir string) []mcpconfig.Source {
	dir = filepath.Join("testdata", dir)
	return []mcpconfig.Source{
		{Agent: mcpconfig.Claude, Path: filepath.Join(dir, ".claude.json")},
		{Agent: mcpconfig.Codex, Path: filepath.Join(dir, "config.toml")},
	}
}

func TestFindIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		wantURL string
		wantKey string
		wantSrc mcpconfig.Agent
	}{
		{name: "only Claude Code", dir: "claude-only", wantURL: claudeURL, wantKey: claudeKey, wantSrc: mcpconfig.Claude},
		{name: "only Codex with http_headers", dir: "codex-headers", wantURL: codexURL, wantKey: codexKey, wantSrc: mcpconfig.Codex},
		{name: "both configs: Claude Code wins", dir: "both", wantURL: claudeURL, wantKey: claudeKey, wantSrc: mcpconfig.Claude},
		{name: "broken JSON falls back to Codex", dir: "broken-json", wantURL: codexURL, wantKey: codexKey, wantSrc: mcpconfig.Codex},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srcs := sources(tt.dir)
			url, key, src, err := mcpconfig.FindIn(srcs...)
			if err != nil {
				t.Fatalf("FindIn: %v", err)
			}
			if url != tt.wantURL || key != tt.wantKey {
				t.Errorf("got %q %q, want %q %q", url, key, tt.wantURL, tt.wantKey)
			}
			if src.Agent != tt.wantSrc {
				t.Errorf("source agent = %q, want %q", src.Agent, tt.wantSrc)
			}
			for _, s := range srcs {
				if s.Agent == tt.wantSrc && src.Path != s.Path {
					t.Errorf("source path = %q, want %q", src.Path, s.Path)
				}
			}
		})
	}
}

func TestFindInReportsEveryConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		wantClaude error
		wantCodex  error
		// wantText is a part of the message a person reads.
		wantText string
	}{
		{
			name: "server in neither config", dir: "none",
			wantClaude: mcpconfig.ErrNoServer, wantCodex: mcpconfig.ErrNoServer,
			wantText: "MCP server hottell is not in the config",
		},
		{
			name: "no config files", dir: "missing",
			wantClaude: mcpconfig.ErrNoServer, wantCodex: mcpconfig.ErrNoServer,
			wantText: "no such file",
		},
		{
			name: "URL without key", dir: "claude-no-key",
			wantClaude: mcpconfig.ErrNoKey, wantCodex: mcpconfig.ErrNoServer,
			wantText: "has a url but no Authorization header",
		},
		{
			name: "Codex key in bearer_token_env_var", dir: "codex-bearer-env",
			wantClaude: mcpconfig.ErrNoServer, wantCodex: mcpconfig.ErrKeyInEnv,
			wantText: "not in http_headers",
		},
		{
			name: "Claude Code server not of type http", dir: "claude-not-http",
			wantClaude: mcpconfig.ErrNotHTTP, wantCodex: mcpconfig.ErrNoServer,
			wantText: `type "stdio"`,
		},
		{
			name: "Claude Code server only in project scope", dir: "claude-local-only",
			wantClaude: mcpconfig.ErrNoServer, wantCodex: mcpconfig.ErrNoServer,
			wantText: "MCP server hottell is not in the config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srcs := sources(tt.dir)
			url, key, _, err := mcpconfig.FindIn(srcs...)
			if err == nil {
				t.Fatalf("FindIn found %q %q, want an error", url, key)
			}
			var notFound *mcpconfig.NotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("error %v is not a *NotFoundError", err)
			}
			if len(notFound.Configs) != 2 {
				t.Fatalf("got %d config reasons, want 2: %v", len(notFound.Configs), err)
			}
			for i, want := range []error{tt.wantClaude, tt.wantCodex} {
				got := notFound.Configs[i]
				if got.Source != srcs[i] {
					t.Errorf("reason %d is for %v, want %v", i, got.Source, srcs[i])
				}
				if !errors.Is(got, want) {
					t.Errorf("%s reason = %v, want %v", got.Source.Agent, got.Err, want)
				}
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("message %q does not contain %q", err, tt.wantText)
			}
			for _, s := range srcs {
				if !strings.Contains(err.Error(), s.Path) {
					t.Errorf("message %q does not name %s", err, s.Path)
				}
			}
		})
	}
}

func TestFindInBrokenConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      mcpconfig.Source
		wantText string
	}{
		{name: "broken JSON", src: sources("broken-json")[0], wantText: "parse JSON"},
		{name: "broken TOML", src: sources("broken-toml")[1], wantText: "parse TOML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := mcpconfig.FindIn(tt.src)
			if err == nil {
				t.Fatal("FindIn succeeded on a broken config")
			}
			if !strings.Contains(err.Error(), tt.wantText) || !strings.Contains(err.Error(), tt.src.Path) {
				t.Errorf("message %q does not contain %q and %s", err, tt.wantText, tt.src.Path)
			}
		})
	}
}

//nolint:paralleltest // sets HOME, CLAUDE_CONFIG_DIR and CODEX_HOME
func TestFindReadsDefaultConfigs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(mcpconfig.ClaudeConfigDirEnv, "")
	t.Setenv(mcpconfig.CodexHomeEnv, filepath.Join("testdata", "codex-headers"))

	url, key, src, err := mcpconfig.Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if url != codexURL || key != codexKey || src.Agent != mcpconfig.Codex {
		t.Errorf("got %q %q from %v, want the Codex entry", url, key, src)
	}

	t.Setenv(mcpconfig.ClaudeConfigDirEnv, filepath.Join("testdata", "both"))
	url, key, src, err = mcpconfig.Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if url != claudeURL || key != claudeKey || src.Agent != mcpconfig.Claude {
		t.Errorf("got %q %q from %v, want the Claude Code entry", url, key, src)
	}
}

//nolint:paralleltest // sets HOME, CLAUDE_CONFIG_DIR and CODEX_HOME
func TestDefaultSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(mcpconfig.ClaudeConfigDirEnv, "")
	t.Setenv(mcpconfig.CodexHomeEnv, "")

	got, err := mcpconfig.DefaultSources()
	if err != nil {
		t.Fatalf("DefaultSources: %v", err)
	}
	want := []mcpconfig.Source{
		{Agent: mcpconfig.Claude, Path: filepath.Join(home, ".claude.json")},
		{Agent: mcpconfig.Codex, Path: filepath.Join(home, ".codex", "config.toml")},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("DefaultSources = %v, want %v", got, want)
	}
}

//nolint:paralleltest // sets HOME, CLAUDE_CONFIG_DIR and CODEX_HOME
func TestFindExplicitPathReadsOnlyThatFile(t *testing.T) {
	// Both defaults hold a usable entry; an explicit path must ignore them.
	both := filepath.Join("testdata", "both")
	t.Setenv("HOME", t.TempDir())
	t.Setenv(mcpconfig.ClaudeConfigDirEnv, both)
	t.Setenv(mcpconfig.CodexHomeEnv, both)

	path := filepath.Join("testdata", "codex-headers", "config.toml")
	url, key, src, err := mcpconfig.Find(path)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if url != codexURL || key != codexKey || src != (mcpconfig.Source{Agent: mcpconfig.Codex, Path: path}) {
		t.Errorf("got %q %q from %v, want the Codex entry of %s", url, key, src, path)
	}

	path = filepath.Join("testdata", "none", ".claude.json")
	_, _, _, err = mcpconfig.Find(path)
	var notFound *mcpconfig.NotFoundError
	if !errors.As(err, &notFound) || len(notFound.Configs) != 1 || notFound.Configs[0].Source.Path != path {
		t.Errorf("Find(%s) = %v, want a *NotFoundError for that file alone", path, err)
	}
}
