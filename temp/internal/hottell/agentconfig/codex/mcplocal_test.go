package codex_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
)

const foreignTOML = `model = "gpt"

# сервис команды
[mcp_servers.hottell]
url = "http://localhost:8080/mcp"

[mcp_servers.gitea]
command = "/x/gitea-mcp"

[tui]
theme = "dark"
`

// readConfig returns config.toml under home.
func readConfig(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(codex.ConfigFile(home))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeConfig writes config.toml under home.
func writeConfig(t *testing.T, home, text string) {
	t.Helper()
	if err := os.WriteFile(codex.ConfigFile(home), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPLocal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeConfig(t, home, foreignTOML)
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	text := readConfig(t, home)
	if !strings.HasPrefix(text, strings.TrimRight(foreignTOML, "\n")) ||
		!strings.Contains(text, "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\n") {
		t.Fatalf("after Ensure:\n%s", text)
	}
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if again := readConfig(t, home); again != text {
		t.Fatalf("a repeated Ensure changed the file:\n%s", again)
	}
	if err := codex.RemoveMCPLocal(home, "hottell-local"); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, home); got != foreignTOML {
		t.Fatalf("after Remove the file is not the original byte for byte:\n%s", got)
	}
}

// TestMCPLocalKeepsUserKeys: hottell owns only command and args of an existing table; the
// user's keys, comments and sub-tables stay, and the table stays where it is.
func TestMCPLocalKeepsUserKeys(t *testing.T) {
	t.Parallel()
	const before = `model = "gpt"

[mcp_servers.hottell-local]
command = "/old/hottell" # путь бинаря
enabled = true
args = ["mcp-local"]
tool_timeout_sec = 120

[mcp_servers.hottell-local.env]
HOTTELL_DATA_DIR = "/data"

[tui]
theme = "dark"
`
	home := t.TempDir()
	writeConfig(t, home, before)
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(before, `command = "/old/hottell"`, `command = "/u/.local/bin/hottell"`, 1)
	if got := readConfig(t, home); got != want {
		t.Fatalf("after Ensure:\n%s\nwant:\n%s", got, want)
	}
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, home); got != want {
		t.Fatalf("a repeated Ensure changed the file:\n%s", got)
	}
}

// TestMCPLocalCompletesTable: a table without command or args, or with args of its own,
// gets ours in place.
func TestMCPLocalCompletesTable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, before, want string }{
		{
			name:   "neither",
			before: "[mcp_servers.hottell-local]\nenabled = false\n\n[tui]\ntheme = \"dark\"\n",
			want:   "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\nenabled = false\n\n[tui]\ntheme = \"dark\"\n",
		},
		{
			name:   "no args",
			before: "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nenabled = false\n",
			want:   "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\nenabled = false\n",
		},
		{
			name:   "no command",
			before: "[mcp_servers.hottell-local]\nargs = [\"mcp-local\"]\n",
			want:   "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\n",
		},
		{
			name:   "other args over lines",
			before: "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\n  \"serve\",\n] # свои\nenabled = true\n",
			want:   "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"] # свои\nenabled = true\n",
		},
		{
			name:   "header at the end without a newline",
			before: "model = \"gpt\"\n[mcp_servers.hottell-local]",
			want:   "model = \"gpt\"\n[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\n",
		},
		{
			name:   "only a sub-table",
			before: "[mcp_servers.hottell-local.env]\nX = \"1\"\n",
			want:   "[mcp_servers.hottell-local.env]\nX = \"1\"\n\n[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\n",
		},
	} {
		home := t.TempDir()
		writeConfig(t, home, tc.before)
		if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := readConfig(t, home); got != tc.want {
			t.Errorf("%s: after Ensure:\n%s\nwant:\n%s", tc.name, got, tc.want)
		}
	}
}

// TestMCPLocalRemovesSubTables: Remove takes the table out with its sub-tables and the
// blank line above it.
func TestMCPLocalRemovesSubTables(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	own := "[mcp_servers.hottell-local]\ncommand = \"/old/hottell\"\nargs = [\"mcp-local\"]\n\n[mcp_servers.hottell-local.env]\nX = \"1\"\n"
	writeConfig(t, home, "model = \"gpt\"\n\n"+own+"\n[tui]\ntheme = \"dark\"\n")
	if err := codex.RemoveMCPLocal(home, "hottell-local"); err != nil {
		t.Fatal(err)
	}
	if got, want := readConfig(t, home), "model = \"gpt\"\n\n[tui]\ntheme = \"dark\"\n"; got != want {
		t.Fatalf("after Remove:\n%s\nwant:\n%s", got, want)
	}
}

// TestMCPLocalRefusesDefinitionOutsideTable: mcp_servers.<name>, or mcp_servers itself,
// written outside a [mcp_servers.<name>] table is the user's. Ensure refuses it rather than
// write a table Codex can no longer parse; Remove leaves it and succeeds.
func TestMCPLocalRefusesDefinitionOutsideTable(t *testing.T) {
	t.Parallel()
	for _, config := range []string{
		"[mcp_servers]\nhottell-local = { command = \"x\" }\n",
		"mcp_servers.hottell-local.command = \"x\"\n",
		"[[mcp_servers.hottell-local]]\ncommand = \"x\"\n",
		"mcp_servers = { gitea = { command = \"/x/gitea-mcp\" } }\n",
		"mcp_servers = {}\n",
		"[[mcp_servers]]\ncommand = \"x\"\n",
	} {
		home := t.TempDir()
		writeConfig(t, home, config)
		if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); !errors.Is(err, codex.ErrMalformed) {
			t.Errorf("%q: Ensure = %v, want ErrMalformed", config, err)
		}
		if got := readConfig(t, home); got != config {
			t.Errorf("%q: Ensure changed the file:\n%s", config, got)
		}
		if err := codex.RemoveMCPLocal(home, "hottell-local"); err != nil {
			t.Errorf("%q: Remove = %v, want nil", config, err)
		}
		if got := readConfig(t, home); got != config {
			t.Errorf("%q: Remove changed the file:\n%s", config, got)
		}
	}
}

func TestMCPLocalMissingConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := codex.RemoveMCPLocal(home, "hottell-local"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(codex.ConfigFile(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove created config.toml: %v", err)
	}
	if err := codex.EnsureMCPLocal(home, "hottell-local", "/u/.local/bin/hottell"); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, home); got != "[mcp_servers.hottell-local]\ncommand = \"/u/.local/bin/hottell\"\nargs = [\"mcp-local\"]\n" {
		t.Fatalf("new config.toml:\n%s", got)
	}
}
