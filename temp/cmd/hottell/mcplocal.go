package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// dataDirEnv moves the dashboard's local-data that hottell-local reads.
const dataDirEnv = "HOTTELL_DATA_DIR"

// runMCPLocal serves hottell-local, the coach's local MCP server, over stdin and stdout.
// The agents start it from their configs; install puts it there.
func runMCPLocal(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	cfg, err := mcpLocalConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell mcp-local: %v\n", err)
		return exitFailure
	}
	if err := mcpserver.Run(context.Background(), cfg, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "hottell mcp-local: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// mcpLocalConfigFrom finds the transcripts where install finds the agents' configs and the
// dashboard's data in HOTTELL_DATA_DIR or ~/Life/projects/ai-hottell/local-data. It needs
// only the agents' locations: a CODEX_HOME that cannot be resolved is used as it is set,
// since the Claude Code transcripts are still there to read.
func mcpLocalConfigFrom(getenv func(string) string) (mcpserver.Config, error) {
	loc, err := agentDirs(getenv)
	if err != nil {
		return mcpserver.Config{}, err
	}
	data := getenv(dataDirEnv)
	if data == "" {
		data = filepath.Join(loc.home, "Life", "projects", "ai-hottell", "local-data")
	}
	return mcpserver.Config{
		Roots:   sessions.Roots{ClaudeProjects: filepath.Join(loc.claudeDir, "projects"), CodexHome: loc.codexHome},
		DataDir: data, Version: currentVersion(), Now: time.Now,
	}, nil
}
