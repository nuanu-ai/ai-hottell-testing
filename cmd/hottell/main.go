// Command hottell collects telemetry of Claude Code and Codex on the user's Mac.
//
// Usage:
//
//	hottell install                install the binary, the agents' hooks and OTel, hottell-local and launchd
//	hottell uninstall              remove everything install set up
//	hottell restore [--list|<set>] put the agents' configs back from a backup, offline
//	hottell status [--json]        show what is installed and running; exits 1 on a problem
//	hottell hook claude|codex      handle one hook event of the agent; always exits 0
//	hottell daemon                 run the background process under launchd
//	hottell mcp-local              serve hottell-local, the coach's local MCP server, over stdio
//	hottell version                print the version
//
// hottell-local is the coach's local MCP server: install registers hottell mcp-local in the
// configs of the agents that are installed (mcpServers.hottell-local of ~/.claude.json,
// [mcp_servers.hottell-local] of config.toml) and uninstall removes it. It reads only this
// Mac's transcripts and the dashboard's data, never the service: the section on
// hottell-local in docs/specs/hottell-contract/mcp.md.
//
// A call whose first argument is a flag is a hook call: the prototype's hooks still call
// hottell -agent claude|codex [-config <path>], which is handled as hottell hook; any other
// flag call except -h and --help is a hook call without an agent and also exits 0.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

const usage = "usage: hottell install|uninstall|daemon|mcp-local|version | hottell status [--json] | hottell restore [--list|<set>] | hottell hook claude|codex"

// Exit codes: a failed command and a malformed invocation.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// version is set at build time with -ldflags "-X main.version=..."; without it the
// version comes from the build info.
//
//nolint:gochecknoglobals // the linker can only set a package-level variable
var version string

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "-h", "-help", "--help":
		fmt.Fprintln(stderr, usage)
		return exitOK
	}
	if strings.HasPrefix(args[0], "-") {
		// No subcommand starts with a flag, so this is an agent hook, which exit 2 would block.
		return runHook(legacyHookArgs(args), stdin, getenv)
	}

	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, currentVersion())
		return exitOK
	case "hook":
		// A hook never fails the agent and never writes to stdout, which the agent reads.
		return runHook(args[1:], stdin, getenv)
	case "daemon":
		return runDaemon(args[1:], stderr, getenv)
	case "mcp-local":
		return runMCPLocal(args[1:], stdin, stdout, stderr, getenv)
	case "install":
		return runInstall(args[1:], stdout, stderr, getenv)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr, getenv)
	case "status":
		return runStatus(args[1:], stdout, stderr, getenv)
	case "restore":
		return runRestore(args[1:], stdout, stderr, getenv)
	default:
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
}

// currentVersion is the version set by the linker, or dev+<commit> from the build info.
func currentVersion() string {
	if version != "" {
		return version
	}
	info, _ := debug.ReadBuildInfo()
	return devVersion(info)
}

// devVersion is dev+<commit> of the VCS revision recorded in info, or dev without one.
func devVersion(info *debug.BuildInfo) string {
	if info == nil {
		return "dev"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return "dev+" + setting.Value
		}
	}
	return "dev"
}
