package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPLocalConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"HOME": home}
	cfg, err := mcpLocalConfigFrom(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roots.ClaudeProjects != filepath.Join(home, ".claude", "projects") || cfg.Roots.CodexHome != filepath.Join(home, ".codex") ||
		cfg.DataDir != filepath.Join(home, "Life", "projects", "ai-hottell", "local-data") {
		t.Fatalf("defaults: %+v", cfg)
	}
	env["HOTTELL_DATA_DIR"] = "/x/data"
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(home, "cc")
	cfg, err = mcpLocalConfigFrom(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/x/data" || cfg.Roots.ClaudeProjects != filepath.Join(home, "cc", "projects") {
		t.Fatalf("from the environment: %+v", cfg)
	}
}

// TestMCPLocalConfigCodexHome: CODEX_HOME is resolved through its symlinks the way Codex
// does; one that cannot be resolved is used as it is set, since the Claude Code
// transcripts are still readable, while the daemon refuses it.
func TestMCPLocalConfigCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := filepath.Join(home, "codex-real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "codex-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mcpLocalConfigFrom(lookup(map[string]string{"HOME": home, "CODEX_HOME": link}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roots.CodexHome != resolved {
		t.Errorf("CODEX_HOME through a symlink = %q, want %q", cfg.Roots.CodexHome, resolved)
	}

	missing := filepath.Join(home, "no-codex")
	env := map[string]string{"HOME": home, "CODEX_HOME": missing}
	cfg, err = mcpLocalConfigFrom(lookup(env))
	if err != nil {
		t.Fatalf("an unresolvable CODEX_HOME: %v", err)
	}
	if cfg.Roots.CodexHome != missing || cfg.Roots.ClaudeProjects != filepath.Join(home, ".claude", "projects") {
		t.Errorf("an unresolvable CODEX_HOME: %+v", cfg.Roots)
	}
	if _, err := daemonConfigFrom(lookup(env)); err == nil {
		t.Error("the daemon takes an unresolvable CODEX_HOME")
	}
}

func TestMCPLocalConfigWithoutHome(t *testing.T) {
	t.Parallel()
	if _, err := mcpLocalConfigFrom(lookup(nil)); err == nil {
		t.Fatal("no error without HOME")
	}
}

func TestRunMCPLocalUsage(t *testing.T) {
	t.Parallel()
	if code := run([]string{"mcp-local", "extra"}, nil, io.Discard, io.Discard, lookup(nil)); code != exitUsage {
		t.Fatalf("an extra argument: exit code %d, want %d", code, exitUsage)
	}
}
