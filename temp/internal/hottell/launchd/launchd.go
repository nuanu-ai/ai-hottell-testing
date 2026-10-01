// Package launchd runs hottell daemon as a per-user launchd agent: it writes the agent's
// plist to ~/Library/LaunchAgents, loads it into the user's gui domain, reports whether
// it runs and removes it.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Label names the agent in launchd and its plist file.
const Label = "dev.alva.hottell"

// throttleSeconds is the least time launchd waits between two starts of the agent.
const throttleSeconds = 10

// launchctl exit codes that mean the service is not loaded: "No such process" on
// current macOS and "Could not find service" on older ones.
const (
	codeNoSuchProcess   = 3
	codeServiceNotFound = 113
)

// codeIO is the exit code of bootstrap while a service booted out a moment ago is still
// going away.
const codeIO = 5

// Bootstrap retries while the previous instance of the agent is still going away.
const (
	bootstrapAttempts = 5
	bootstrapDelay    = 200 * time.Millisecond
)

// Runner runs launchctl with args and returns its combined output and exit code.
// It returns an error only when launchctl could not be run at all.
type Runner interface {
	Run(ctx context.Context, args ...string) (out []byte, code int, err error)
}

// ExecRunner runs the launchctl binary at Path, or /bin/launchctl when Path is empty.
type ExecRunner struct {
	Path string
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, int, error) {
	path := r.Path
	if path == "" {
		path = "/bin/launchctl"
	}

	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
		return out, exitErr.ExitCode(), nil
	}
	if err != nil {
		return out, 0, fmt.Errorf("run launchctl: %w", err)
	}
	return out, 0, nil
}

// Agent is the launchd agent of one user.
type Agent struct {
	// Home is the user's home directory.
	Home string
	// Binary is the absolute path of the hottell binary the agent runs.
	Binary string
	// UID is the user's id; the agent lives in the gui/<UID> domain.
	UID int
	// Runner runs launchctl.
	Runner Runner
}

// Status is the state of the agent in launchd.
type Status struct {
	// Loaded reports whether the agent is loaded into the user's domain.
	Loaded bool
	// PID is the process id of the running agent, 0 when it does not run.
	PID int
	// Exited reports whether the agent has exited at least once; LastExitCode is
	// meaningful only then.
	Exited       bool
	LastExitCode int
}

// PlistPath is the path of the agent's plist.
func (a Agent) PlistPath() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", Label+".plist")
}

// LogDir is the directory the agent's stdout and stderr go to.
func (a Agent) LogDir() string {
	return filepath.Join(a.Home, "Library", "Logs", "hottell")
}

// Plist renders the agent's plist.
func (a Agent) Plist() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	writeKey(&b, "Label")
	writeString(&b, "\t", Label)
	writeKey(&b, "ProgramArguments")
	b.WriteString("\t<array>\n")
	writeString(&b, "\t\t", a.Binary)
	writeString(&b, "\t\t", "daemon")
	b.WriteString("\t</array>\n")
	writeKey(&b, "RunAtLoad")
	b.WriteString("\t<true/>\n")
	writeKey(&b, "KeepAlive")
	b.WriteString("\t<true/>\n")
	writeKey(&b, "ThrottleInterval")
	fmt.Fprintf(&b, "\t<integer>%d</integer>\n", throttleSeconds)
	writeKey(&b, "StandardOutPath")
	writeString(&b, "\t", filepath.Join(a.LogDir(), "stdout.log"))
	writeKey(&b, "StandardErrorPath")
	writeString(&b, "\t", filepath.Join(a.LogDir(), "stderr.log"))
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

func writeKey(b *bytes.Buffer, key string) {
	writeElement(b, "\t", "key", key)
}

func writeString(b *bytes.Buffer, indent, s string) {
	writeElement(b, indent, "string", s)
}

func writeElement(b *bytes.Buffer, indent, name, text string) {
	fmt.Fprintf(b, "%s<%s>", indent, name)
	// Writing to a bytes.Buffer does not fail.
	_ = xml.EscapeText(b, []byte(text))
	fmt.Fprintf(b, "</%s>\n", name)
}

// Install writes the plist and (re)loads the agent, so launchd starts it now and at
// every login. Called again, it restarts the agent with the current binary and plist.
func (a Agent) Install(ctx context.Context) error {
	if err := os.MkdirAll(a.LogDir(), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	if err := writeFileAtomic(a.PlistPath(), a.Plist()); err != nil {
		return err
	}
	if err := a.bootout(ctx); err != nil {
		return err
	}
	return a.bootstrap(ctx)
}

// Uninstall stops and unloads the agent and removes its plist. Missing pieces are not
// an error.
func (a Agent) Uninstall(ctx context.Context) error {
	if err := a.bootout(ctx); err != nil {
		return err
	}
	if err := os.Remove(a.PlistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// Status reports whether the agent is loaded, its pid and the code of its last exit.
func (a Agent) Status(ctx context.Context) (Status, error) {
	out, code, err := a.Runner.Run(ctx, "print", a.service())
	if err != nil {
		return Status{}, err
	}
	if isNotLoaded(code) {
		return Status{}, nil
	}
	if code != 0 {
		return Status{}, launchctlError("print", code, out)
	}
	return parsePrint(out)
}

func (a Agent) domain() string {
	return "gui/" + strconv.Itoa(a.UID)
}

func (a Agent) service() string {
	return a.domain() + "/" + Label
}

func (a Agent) bootout(ctx context.Context) error {
	out, code, err := a.Runner.Run(ctx, "bootout", a.service())
	if err != nil {
		return err
	}
	if code != 0 && !isNotLoaded(code) {
		return launchctlError("bootout", code, out)
	}
	return nil
}

func (a Agent) bootstrap(ctx context.Context) error {
	for attempt := 1; ; attempt++ {
		out, code, err := a.Runner.Run(ctx, "bootstrap", a.domain(), a.PlistPath())
		if err != nil {
			return err
		}
		if code == 0 {
			return nil
		}
		if code != codeIO || attempt == bootstrapAttempts {
			return launchctlError("bootstrap", code, out)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("launchctl bootstrap: %w", ctx.Err())
		case <-time.After(bootstrapDelay):
		}
	}
}

func isNotLoaded(code int) bool {
	return code == codeNoSuchProcess || code == codeServiceNotFound
}

func launchctlError(verb string, code int, out []byte) error {
	return fmt.Errorf("launchctl %s: exit code %d: %s", verb, code, strings.TrimSpace(string(out)))
}

// parsePrint reads the top-level "pid" and "last exit code" fields of launchctl print.
// Nested dictionaries are indented deeper and ignored.
func parsePrint(out []byte) (Status, error) {
	st := Status{Loaded: true}
	for line := range strings.Lines(string(out)) {
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}

		switch key {
		case "pid":
			pid, err := strconv.Atoi(value)
			if err != nil {
				return Status{}, fmt.Errorf("parse pid %q: %w", value, err)
			}
			st.PID = pid
		case "last exit code":
			if value == "(never exited)" {
				continue
			}
			// The code may be followed by its name, as in "78: EX_CONFIG".
			num, _, _ := strings.Cut(value, ":")
			exitCode, err := strconv.Atoi(num)
			if err != nil {
				return Status{}, fmt.Errorf("parse last exit code %q: %w", value, err)
			}
			st.Exited, st.LastExitCode = true, exitCode
		}
	}
	return st, nil
}

// writeFileAtomic replaces path with data, so launchd never reads a half-written plist.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp plist: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck,gosec // the write error is the one to report
		return fmt.Errorf("write plist: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close() //nolint:errcheck,gosec // the chmod error is the one to report
		return fmt.Errorf("chmod plist: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close plist: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename plist: %w", err)
	}
	return nil
}
