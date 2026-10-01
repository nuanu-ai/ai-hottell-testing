package mcpclient_test

import (
	"os"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// TestLivePing connects with the hottell entry of the real agents' configs
// (~/.claude.json, then ~/.codex/config.toml, or CLAUDE_CONFIG_DIR and CODEX_HOME) and
// calls ping, so it runs only when HOTTELL_MCP_LIVE=1. The configs are only read; the
// status goes to a temporary state, and neither the key nor the email is printed.
func TestLivePing(t *testing.T) {
	t.Parallel()
	if os.Getenv("HOTTELL_MCP_LIVE") != "1" {
		t.Skip("set HOTTELL_MCP_LIVE=1 to ping the service with the key of the agent's config")
	}

	c := mcpclient.New(mcpclient.Config{Paths: state.PathsIn(t.TempDir()), Version: "live-test"})
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %s: %v", mcpclient.ReasonOf(err), err)
	}
	info, err := c.Ping(t.Context())
	if err != nil {
		t.Fatalf("Ping: %s: %v", mcpclient.ReasonOf(err), err)
	}
	if info.Version == "" || info.Email == "" {
		t.Fatalf("Ping answered without a version or an email")
	}
	t.Logf("ping ok, service version %s", info.Version)
}
