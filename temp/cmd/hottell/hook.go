package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// offEnv set to 1 turns the hook off: the event is read and dropped.
const offEnv = "HOTTELL_OFF"

// hookLog is the file in the logs directory where the hook reports its errors.
const hookLog = "hook.log"

// errUnknownAgent means hottell hook was called without claude or codex.
var errUnknownAgent = errors.New("unknown agent")

// runHook handles one hook event of the agent named in args. It never fails the agent:
// it always returns exitOK, never writes to stdout and never goes to the network; an
// error or a panic becomes a line in the hook log.
func runHook(args []string, stdin io.Reader, getenv func(string) string) (code int) {
	// Without paths there is no log either.
	paths, home, pathsErr := hookPaths(getenv)
	defer func() {
		if r := recover(); r != nil {
			logHookError(paths, args, fmt.Errorf("panic: %v", r))
			code = exitOK
		}
	}()

	event, err := io.ReadAll(stdin)
	receivedAt := time.Now()
	if getenv(offEnv) == "1" || pathsErr != nil {
		return exitOK
	}
	if err != nil {
		logHookError(paths, args, fmt.Errorf("read stdin: %w", err))
		return exitOK
	}
	if err := handleHook(paths, home, args, event, receivedAt); err != nil {
		logHookError(paths, args, err)
	}
	return exitOK
}

func handleHook(paths state.Paths, home string, args []string, event []byte, receivedAt time.Time) error {
	if len(args) != 1 || (args[0] != string(policy.Claude) && args[0] != string(policy.Codex)) {
		return errUnknownAgent
	}
	cache, err := paths.ReadSettings()
	if err != nil {
		// The denials are unknown, so nothing is sent.
		return fmt.Errorf("read settings: %w", err)
	}
	settings := policy.Settings{Home: home}
	if cache.Document != nil {
		if settings, err = policy.Parse(cache.Document, home); err != nil {
			return err
		}
	}
	q := queue.Open(paths, 0, nil)
	return hook.Handle(settings, policy.Agent(args[0]), event, receivedAt, q)
}

// legacyHookArgs turns the prototype's hook flags into the args of hottell hook: the value
// of -agent, --agent, -agent= or --agent=, the last one winning. -config and every other
// flag are ignored; a -config value is skipped. Without an agent the args are empty.
func legacyHookArgs(flags []string) []string {
	var agent []string
	for i := 0; i < len(flags); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(flags[i], "-"), "=")
		if name != "agent" && name != "config" {
			continue
		}
		if !hasValue {
			if i+1 >= len(flags) {
				break
			}
			i++
			value = flags[i]
		}
		if name == "agent" {
			agent = []string{value}
		}
	}
	return agent
}

// hookPaths resolves the state locations and the home directory from the environment.
func hookPaths(getenv func(string) string) (state.Paths, string, error) {
	home := getenv("HOME")
	if root := getenv(state.HomeEnv); root != "" {
		return state.PathsIn(root), home, nil
	}
	if home == "" {
		return state.Paths{}, "", errors.New("HOME is not set")
	}
	return state.PathsForHome(home), home, nil
}

// logHookError appends a line about err to the hook log; a log that cannot be written is
// given up on, since the hook has nowhere else to report.
func logHookError(paths state.Paths, args []string, err error) {
	if paths.Logs == "" {
		return
	}
	if mkErr := os.MkdirAll(paths.Logs, 0o700); mkErr != nil {
		return
	}
	f, openErr := os.OpenFile(filepath.Join(paths.Logs, hookLog), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if openErr != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s hook %q: %v\n", time.Now().UTC().Format(time.RFC3339), args, err)
}
