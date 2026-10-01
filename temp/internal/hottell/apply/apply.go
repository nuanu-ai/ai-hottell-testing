// Package apply brings the configuration of both agents in line with a version of the
// settings and the collector credentials: the hottell hooks, the trust of the Codex
// hooks and the native OpenTelemetry. The daemon calls it whenever the settings
// subscription brings a new version or a new collector token.
//
// The deny settings of hooks and transcripts are not applied here: the hook and the
// transcript reader apply them to every event (policy). A denied agent keeps its hooks,
// which drop its events, and gets its native OpenTelemetry switched off. A folder
// denial writes nothing into the folder: neither agent reads OpenTelemetry keys from
// project settings (docs/specs/hottell-contract/native-otel.md, section 4).
package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

// Steps of an agent, in the order they run.
const (
	StepHooks = "hooks"
	StepTrust = "trust"
	StepOTel  = "otel"
)

// Status of an agent or of a step.
const (
	// StatusOK means the step brought the file in line.
	StatusOK = "ok"
	// StatusFailed means the step returned the error of the result.
	StatusFailed = "failed"
	// StatusSkipped means the step did not run because a step it needs failed.
	StatusSkipped = "skipped"
	// StatusNotInstalled means the agent's configuration directory does not exist, so
	// nothing of it was touched.
	StatusNotInstalled = "not_installed"
)

// Applier knows where the agents keep their configuration and what to write there.
type Applier struct {
	// ClaudeDir is ~/.claude; Claude Code counts as installed when it exists.
	ClaudeDir string
	// CodexHome is the Codex home as codex.Home resolves it; Codex counts as installed
	// when it exists.
	CodexHome string
	// BinaryPath is the absolute path of the hottell binary the hooks run.
	BinaryPath string
	// Paths are the local state: the records of what hottell wrote, and the result.
	Paths state.Paths
	// Resource names this machine and this binary in the Claude Code resource
	// attributes.
	Resource claude.Resource
	// Backup, when set, copies the agents' configs into a new set before Apply writes, if
	// someone other than hottell changed them since its last write.
	Backup *backup.Store
}

// Result is the outcome of one Apply, kept in the state for hottell status.
type Result struct {
	// Version is the version of the settings applied.
	Version int64 `json:"version"`
	// Agents holds the outcome of each agent.
	Agents map[policy.Agent]AgentResult `json:"agents"`
}

// AgentResult is the outcome of one agent.
type AgentResult struct {
	// Status is StatusNotInstalled, StatusFailed when any step failed, or StatusOK.
	Status string `json:"status"`
	// Steps are the steps that ran or were skipped, in order; none when the agent is not
	// installed.
	Steps []StepResult `json:"steps,omitempty"`
}

// StepResult is the outcome of one step.
type StepResult struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Err returns the errors of the failed steps of every agent joined, or nil.
func (r Result) Err() error {
	var errs []error
	for _, agent := range []policy.Agent{policy.Claude, policy.Codex} {
		for _, step := range r.Agents[agent].Steps {
			if step.Status == StatusFailed {
				errs = append(errs, fmt.Errorf("%s %s: %s", agent, step.Step, step.Error))
			}
		}
	}
	return errors.Join(errs...)
}

// Apply brings every installed agent in line with settings and creds and saves the
// result in the state. A step that fails is recorded and the others still run, so a
// failure of one agent never stops the other. Every step leaves a file that already
// holds what it wants unwritten, and the result is saved only when it differs from the
// saved one, so a repeated call with the same inputs writes nothing. The error is that
// of the backup or of saving the result; the failures of the steps are in the result.
// Nothing is applied when the backup fails, so that no config is changed without a copy.
func (a Applier) Apply(settings policy.Settings, creds state.Credentials) (Result, error) {
	if err := a.backUp(); err != nil {
		return Result{Version: settings.Version}, fmt.Errorf("back up the agents' configs: %w", err)
	}
	result := Result{
		Version: settings.Version,
		Agents: map[policy.Agent]AgentResult{
			policy.Claude: a.claude(settings, creds),
			policy.Codex:  a.codex(settings, creds),
		},
	}
	err := a.save(result)
	if a.Backup != nil {
		// What the steps left is hottell's own write, which is no reason for the next set.
		err = errors.Join(err, a.Backup.Record())
	}
	return result, err
}

// backUp takes an apply set when someone other than hottell changed an agent's config
// since hottell's last write.
func (a Applier) backUp() error {
	if a.Backup == nil {
		return nil
	}
	changed, err := a.Backup.Changed()
	if err != nil || !changed {
		return err
	}
	_, err = a.Backup.Take(backup.ReasonApply)
	return err
}

func (a Applier) claude(settings policy.Settings, creds state.Credentials) AgentResult {
	installed, err := isDir(a.ClaudeDir)
	if !installed && err == nil {
		return AgentResult{Status: StatusNotInstalled}
	}
	settingsPath := filepath.Join(a.ClaudeDir, "settings.json")
	otel := claude.OTel{SettingsPath: settingsPath, RecordPath: a.Paths.ClaudeOTelFile(), Resource: a.Resource}
	return agentResult(
		run(StepHooks, err, func() error { return claude.EnsureHooks(settingsPath, a.BinaryPath) }),
		run(StepOTel, err, func() error { return otel.Apply(creds, settings) }),
	)
}

func (a Applier) codex(settings policy.Settings, creds state.Credentials) AgentResult {
	installed, err := isDir(a.CodexHome)
	if !installed && err == nil {
		return AgentResult{Status: StatusNotInstalled}
	}
	hooks := run(StepHooks, err, func() error { return codex.EnsureHooks(a.CodexHome, a.BinaryPath) })
	// The trust is computed from the hooks as they are in hooks.json, so it waits for
	// them.
	trust := StepResult{Step: StepTrust, Status: StatusSkipped}
	if hooks.Status == StatusOK {
		trust = run(StepTrust, nil, func() error { return codex.EnsureTrust(a.CodexHome, a.BinaryPath) })
	}
	return agentResult(
		hooks,
		trust,
		run(StepOTel, err, func() error { return codex.ApplyOTel(a.CodexHome, a.Paths, creds, settings) }),
	)
}

// run runs step unless the agent's directory could not be examined, which fails it.
func run(step string, dirErr error, do func() error) StepResult {
	err := dirErr
	if err == nil {
		err = do()
	}
	if err != nil {
		return StepResult{Step: step, Status: StatusFailed, Error: err.Error()}
	}
	return StepResult{Step: step, Status: StatusOK}
}

func agentResult(steps ...StepResult) AgentResult {
	status := StatusOK
	for _, step := range steps {
		if step.Status != StatusOK {
			status = StatusFailed
		}
	}
	return AgentResult{Status: status, Steps: steps}
}

// isDir reports whether dir exists and is a directory; a missing one is no error.
func isDir(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a directory", dir)
	}
	return true, nil
}

// save writes result into the state unless the saved result is the same.
func (a Applier) save(result Result) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode apply result: %w", err)
	}
	old, err := os.ReadFile(a.Paths.ApplyFile())
	if err == nil && string(old) == string(data) {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", a.Paths.ApplyFile(), err)
	}
	return state.WriteFileAtomic(a.Paths.ApplyFile(), data)
}

// ReadResult returns the result of the last Apply saved under paths, and false when none
// is saved.
func ReadResult(paths state.Paths) (Result, bool, error) {
	data, err := os.ReadFile(paths.ApplyFile())
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("read %s: %w", paths.ApplyFile(), err)
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, false, fmt.Errorf("decode %s: %w", paths.ApplyFile(), err)
	}
	return result, true, nil
}
