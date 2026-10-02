package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/skills"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

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

// mcpLocalConfigFrom finds the transcripts where install finds the agents' configs. It needs
// only the agents' locations: a CODEX_HOME that cannot be resolved is used as it is set,
// since the Claude Code transcripts are still there to read. It shows only the sessions
// the settings let hottell send (allowedTranscripts).
func mcpLocalConfigFrom(getenv func(string) string) (mcpserver.Config, error) {
	loc, err := agentDirs(getenv)
	if err != nil {
		return mcpserver.Config{}, err
	}
	paths, home, err := hookPaths(getenv)
	if err != nil {
		return mcpserver.Config{}, err
	}
	return mcpserver.Config{
		Roots: sessions.Roots{
			ClaudeProjects: filepath.Join(loc.claudeDir, "projects"), CodexHome: loc.codexHome,
			NewAllow: allowedTranscripts(paths, home),
		},
		Version: currentVersion(), Now: time.Now,
		Skills: func(now time.Time) []mcpserver.SkillItem { return skillsInventory(home, now) },
	}, nil
}

// skillRoots are the roots skills_inventory reads, in the order a name found in two of them
// is taken (mcp.md, «skills_inventory»): the global roots of both agents, the Codex
// runtime's own ~/.codex/skills/.system left out.
var skillRoots = []string{"~/.codex/skills/", "~/.agents/skills/", "~/.claude/skills/"} //nolint:gochecknoglobals // a fixed list, never written

// skillsInventory lists the skills skills.Collect finds under home's global roots of both
// agents, for skills_inventory: the snapshot the daemon sends as hottell-skills, narrowed to
// what the coach may propose changes to. A skill with no description, one under no root of
// skillRoots and the second of one name are left out; the list goes by name.
func skillsInventory(home string, now time.Time) []mcpserver.SkillItem {
	type found struct {
		item mcpserver.SkillItem
		root int
	}
	byName := map[string]found{}
	for _, agent := range []policy.Agent{policy.Codex, policy.Claude} {
		for _, it := range skills.Collect(agent, home, "", now).Items {
			root := slices.IndexFunc(skillRoots, func(r string) bool {
				rest, ok := strings.CutPrefix(it.PathClass, r)
				return ok && !strings.Contains(rest, "/")
			})
			if root < 0 || it.Description == "" {
				continue
			}
			if prev, dup := byName[it.Name]; dup && prev.root <= root {
				continue
			}
			sum := sha256.Sum256([]byte(it.Description))
			byName[it.Name] = found{mcpserver.SkillItem{
				Name: it.Name, Description: it.Description, PathClass: it.PathClass,
				DescriptionSHA256: hex.EncodeToString(sum[:]),
			}, root}
		}
	}
	out := make([]mcpserver.SkillItem, 0, len(byName))
	for _, f := range byName {
		out = append(out, f.item)
	}
	slices.SortFunc(out, func(a, b mcpserver.SkillItem) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// allowedTranscripts gives each tool call one snapshot of the settings: the Allow it
// returns lets through a session whose transcript those settings let hottell send
// (policy.Transcript: the agent, its transcripts source and the folder of the cwd). The
// settings have no denial of a single session, so the id is not consulted. The cache is
// read once per call, so one answer never mixes two versions of the settings and a change
// applies from the next call of a server already running; while the denials are unknown —
// no cache, or one that cannot be read or parsed — no session is allowed, as the daemon
// sends nothing then.
func allowedTranscripts(paths state.Paths, home string) func() func(agent, id, cwd string) bool {
	return func() func(agent, id, cwd string) bool {
		deny := func(_, _, _ string) bool { return false }
		cache, err := paths.ReadSettings()
		if err != nil || cache.Document == nil {
			return deny
		}
		settings, err := policy.Parse(cache.Document, home)
		if err != nil {
			return deny
		}
		return func(agent, _, cwd string) bool {
			return policy.Transcript(settings, policy.Agent(agent), cwd, nil)
		}
	}
}
