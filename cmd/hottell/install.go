package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/hookcmd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/apply"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/mcpserver"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/sender"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/skillinstall"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
	"git.alva.dev/alva/harness-telemetry/skills"
)

// Timings of install.
const (
	// testEventWait bounds the wait for the daemon to send the test event.
	testEventWait = 10 * time.Second
	// testEventPoll is how often install looks whether the test event has left the queue.
	testEventPoll = 100 * time.Millisecond
	// mcpTimeout bounds the calls to the MCP server.
	mcpTimeout = 30 * time.Second
)

// The test event install queues: a hook event of its own, which the service stores like
// any other and which shows that the daemon sends.
const (
	testEventName    = "HottellInstall"
	testEventSession = "hottell-install"
)

// binaryPerm is the mode of the installed binary.
const binaryPerm = 0o755

// installConfig is where install puts the binary and how it reaches launchd.
type installConfig struct {
	// daemon is the configuration the installed daemon runs with; its binary is the
	// installed one.
	daemon daemonConfig
	// self is the absolute path of the running binary.
	self string
	// launchd runs launchctl.
	launchd launchd.Runner
	uid     int
	// overrides are the variables that move the state or the agents' configs and are set
	// for install; the daemon under launchd does not get them.
	overrides []string
	// replaceForeign lets install remove the foreign hooks that run the binary at its
	// install path (--replace-foreign-hooks).
	replaceForeign bool

	wait time.Duration
	poll time.Duration
}

// replaceForeignFlag lets install remove the foreign hooks that run the binary at its
// install path.
const replaceForeignFlag = "--replace-foreign-hooks"

// installBinary is where install puts the binary under the home directory.
func installBinary(home string) string {
	return filepath.Join(home, ".local", "bin", "hottell")
}

// runInstall installs the binary, the agents' hooks and native OTel, hottell-local in the
// installed agents and the daemon.
func runInstall(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	replaceForeign := false
	switch {
	case len(args) == 1 && args[0] == replaceForeignFlag:
		replaceForeign = true
	case len(args) != 0:
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	cfg, err := installConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell install: %v\n", err)
		return exitFailure
	}
	cfg.replaceForeign = replaceForeign
	return install(context.Background(), cfg, stdout, stderr)
}

func installConfigFrom(getenv func(string) string) (installConfig, error) {
	daemon, err := daemonConfigFrom(getenv)
	if err != nil {
		return installConfig{}, err
	}
	self := daemon.binary
	daemon.binary = installBinary(daemon.home)
	var overrides []string
	for _, name := range []string{state.HomeEnv, mcpconfig.ClaudeConfigDirEnv, mcpconfig.CodexHomeEnv} {
		if getenv(name) != "" {
			overrides = append(overrides, name)
		}
	}
	return installConfig{
		daemon:    daemon,
		self:      self,
		launchd:   launchd.ExecRunner{},
		uid:       os.Getuid(),
		overrides: overrides,
		wait:      testEventWait,
		poll:      testEventPoll,
	}, nil
}

// install runs the steps in order and prints each outcome, then the steps left to the
// user. A step whose failure leaves nothing for the next ones stops the installation;
// the application to the agents does not, since the daemon applies the settings again.
// Every step leaves what is already in place as it is, so a repeated run reinstalls and
// updates. Before the agents' configs are written they are copied into a backup set, and
// the output ends with the command that puts them back.
func install(ctx context.Context, cfg installConfig, stdout, stderr io.Writer) int {
	r := &report{out: stdout}

	// Hooks of others that run the binary at the install path would run the new one
	// unwrapped; they are removed only with the user's consent, before anything is written.
	// A config that cannot be read for them stops install as well: an empty list from a
	// failed scan is no proof that there are none.
	foreign, err := foreignHooks(cfg.daemon)
	if err != nil {
		r.failed("foreign hooks", err)
		fmt.Fprintln(stderr, "hottell install: stopped, nothing is changed. The agents' configs above cannot be read for hooks of others that run "+
			cfg.daemon.binary+"; fix them and run the installation again.")
		return exitFailure
	}
	if len(foreign) != 0 && !cfg.replaceForeign {
		r.failed("foreign hooks", fmt.Errorf("%d hooks of others run %s, which install replaces", len(foreign), cfg.daemon.binary))
		for _, h := range foreign {
			fmt.Fprintf(stdout, "    %s %s: %s\n", h.file, h.Event, h.Command)
		}
		fmt.Fprintln(stderr, "hottell install: stopped, nothing is changed. Remove these hooks, or run the installation again with "+
			replaceForeignFlag+" to remove them; the configs are backed up first.")
		return exitFailure
	}

	_, _, src, err := mcpconfig.FindIn(cfg.daemon.sources...)
	if err != nil {
		r.failed("mcp", err)
		fmt.Fprintln(stderr, "hottell install: the MCP server hottell is not in the Claude Code or Codex config.")
		fmt.Fprintln(stderr, "Connect it on the page «Подключение» of the service, then run the installation again.")
		return exitFailure
	}
	r.ok("mcp", fmt.Sprintf("%s config %s", src.Agent, src.Path))

	if err := cfg.daemon.paths.Ensure(); err != nil {
		return r.fail(stderr, "state", err)
	}
	settings, err := fetch(ctx, cfg.daemon, r)
	if err != nil {
		return r.fail(stderr, "mcp", err)
	}

	// The agents' configs as they are before install writes them, for hottell restore.
	set, err := agentBackups(cfg.daemon).Take(backup.ReasonInstall)
	if err != nil {
		return r.fail(stderr, "backup", err)
	}
	r.ok("backup", set.Name)
	defer fmt.Fprintf(stdout, "rollback: %s restore %s\n", cfg.daemon.binary, set.Name)

	if len(foreign) != 0 {
		n, err := removeForeignHooks(cfg.daemon)
		if err != nil {
			return r.fail(stderr, "foreign hooks", err)
		}
		r.line("foreign hooks", fmt.Sprintf("removed %d", n), "")
	}

	// The binary is replaced only once nothing runs it unwrapped any more; any step before
	// leaves the binary in place as it was.
	if err := placeBinary(cfg.self, cfg.daemon.binary); err != nil {
		return r.fail(stderr, "binary", err)
	}
	r.ok("binary", cfg.daemon.binary)

	// hottell-local goes in before the settings: its table then stays above the [otel]
	// family, which the settings keep at the end of config.toml, and nothing moves later.
	ensureMCPLocal(cfg.daemon, r)
	// The coach's skills go next to hottell-local, which they use. A failure is reported and
	// install goes on: the hooks and sending do not need the skills.
	reportSkills(r, skillinstall.Install(skills.FS, currentVersion(), skillTargets(cfg.daemon)))
	result := applySettings(cfg.daemon, settings, r)

	agent := launchd.Agent{Home: cfg.daemon.home, Binary: cfg.daemon.binary, UID: cfg.uid, Runner: cfg.launchd}
	if err := agent.Install(ctx); err != nil {
		return r.fail(stderr, "launchd", err)
	}
	r.ok("launchd", agent.PlistPath())
	if len(cfg.overrides) != 0 {
		r.line("environment", "warning", overridesWarning(cfg.overrides))
	}

	testEvent(cfg, settings, src.Agent, r)

	r.manualSteps(result)
	if r.anyFailed {
		fmt.Fprintln(stderr, "hottell install: some steps failed, see above; run the installation again once they are fixed")
		return exitFailure
	}
	return exitOK
}

// overridesWarning tells that the variables in overrides, set for install, do not reach
// the daemon, nor hottell-local unless they are written into the env of its entry.
func overridesWarning(overrides []string) string {
	warning := strings.Join(overrides, ", ") + " set here is not passed to the daemon, which uses the default locations"
	for _, name := range overrides {
		if name == mcpconfig.ClaudeConfigDirEnv || name == mcpconfig.CodexHomeEnv {
			return warning + "; " + mcpserver.Name + " uses them as well unless they are set in the env of its entry"
		}
	}
	return warning
}

// ensureMCPLocal registers hottell-local, the coach's local MCP server, in the agents that
// are installed. A failure is reported but does not stop install: sending works without
// the coach. The change to config.toml is recorded as hottell's own write, so that the next
// application of the settings takes no backup set for it.
func ensureMCPLocal(d daemonConfig, r *report) {
	var in []string
	var errs []error
	register := func(agent, dir string, ensure func() error) {
		installed, err := agentInstalled(dir)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if !installed {
			return
		}
		if err := ensure(); err != nil {
			errs = append(errs, err)
			return
		}
		in = append(in, agent)
	}
	register("Claude Code", d.claudeDir, func() error { return claude.EnsureMCPLocal(claudeJSONOf(d), mcpserver.Name, d.binary) })
	register("Codex", d.codexHome, func() error { return codex.EnsureMCPLocal(d.codexHome, mcpserver.Name, d.binary) })
	switch err := errors.Join(errs...); {
	case err != nil:
		r.failed("mcp-local", err)
	case len(in) == 0:
		r.line("mcp-local", "skipped", "neither agent is installed")
	default:
		r.ok("mcp-local", mcpserver.Name+" in "+strings.Join(in, " and "))
	}
	if err := agentBackups(d).Record(); err != nil {
		r.failed("backup", err)
	}
}

// skillTargets are the agents' homes the skills are laid out in: ~/.codex/skills and
// ~/.claude/skills, or under CODEX_HOME and CLAUDE_CONFIG_DIR.
func skillTargets(d daemonConfig) []skillinstall.Target {
	return []skillinstall.Target{
		{Agent: "Codex", Home: d.codexHome},
		{Agent: "Claude Code", Home: d.claudeDir},
	}
}

// reportSkills prints the skills of both agents as one line: each agent with the outcome of
// each skill, or one failure with every reason.
func reportSkills(r *report, outcomes []skillinstall.Outcome) {
	var agents []string
	byAgent := make(map[string][]string)
	var errs []error
	for _, o := range outcomes {
		if _, seen := byAgent[o.Agent]; !seen {
			agents = append(agents, o.Agent)
		}
		byAgent[o.Agent] = append(byAgent[o.Agent], o.Skill+" "+o.Status)
		if o.Err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", o.Agent, o.Skill, o.Err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		r.failed("skills", err)
		return
	}
	parts := make([]string, 0, len(agents))
	for _, agent := range agents {
		parts = append(parts, agent+": "+strings.Join(byAgent[agent], ", "))
	}
	r.ok("skills", strings.Join(parts, "; "))
}

// agentInstalled reports whether an agent's config directory is there, as the application
// of the settings decides. One that cannot be examined counts, and its edit says why; a
// path that is there but is not a directory is an error, not an agent that is missing.
func agentInstalled(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist), nil
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a directory", dir)
	}
	return true, nil
}

// claudeJSONOf is ~/.claude.json or CLAUDE_CONFIG_DIR/.claude.json, as the MCP sources name it.
func claudeJSONOf(d daemonConfig) string {
	for _, s := range d.sources {
		if s.Agent == mcpconfig.Claude {
			return s.Path
		}
	}
	return filepath.Join(d.home, ".claude.json")
}

// foreignHook is a hook of others in an agent's config that runs the hottell binary at
// its install path.
type foreignHook struct {
	file string
	hookcmd.Hook
}

// foreignHooks lists the foreign hooks of both agents' configs that run cfg.binary.
func foreignHooks(cfg daemonConfig) ([]foreignHook, error) {
	var found []foreignHook
	settings := filepath.Join(cfg.claudeDir, "settings.json")
	claudeHooks, claudeErr := claude.ForeignHooks(settings, cfg.binary, cfg.home)
	for _, h := range claudeHooks {
		found = append(found, foreignHook{file: settings, Hook: h})
	}
	codexHooks, codexErr := codex.ForeignHooks(cfg.codexHome, cfg.binary, cfg.home)
	for _, h := range codexHooks {
		found = append(found, foreignHook{file: codex.HooksFile(cfg.codexHome), Hook: h})
	}
	return found, errors.Join(claudeErr, codexErr)
}

// removeForeignHooks removes the hooks foreignHooks lists from both agents' configs, and
// returns how many it removed.
func removeForeignHooks(cfg daemonConfig) (int, error) {
	fromClaude, claudeErr := claude.RemoveForeignHooks(filepath.Join(cfg.claudeDir, "settings.json"), cfg.binary, cfg.home)
	fromCodex, codexErr := codex.RemoveForeignHooks(cfg.codexHome, cfg.binary, cfg.home)
	return fromClaude + fromCodex, errors.Join(claudeErr, codexErr)
}

// placeBinary copies self to target atomically, unless self already is target.
func placeBinary(self, target string) error {
	same, err := sameFile(self, target)
	if err != nil || same {
		return err
	}
	src, err := os.Open(self) //nolint:gosec // the path of this very binary
	if err != nil {
		return fmt.Errorf("open %s: %w", self, err)
	}
	defer src.Close()
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, binaryPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".hottell.tmp-*")
	if err != nil {
		return fmt.Errorf("create a temporary file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy the binary: %w", err)
	}
	if err := tmp.Chmod(binaryPerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod the binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the binary: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("move the binary to %s: %w", target, err)
	}
	return nil
}

// sameFile reports whether a and b are the same file; a missing b is not.
func sameFile(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", a, err)
	}
	infoB, err := os.Stat(b)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", b, err)
	}
	return os.SameFile(infoA, infoB), nil
}

// fetch saves the collector token and the current settings into the state and returns
// the settings.
func fetch(ctx context.Context, cfg daemonConfig, r *report) (policy.Settings, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpTimeout)
	defer cancel()
	client := mcpclient.New(mcpclient.Config{Paths: cfg.paths, Sources: cfg.sources, Version: cfg.version})
	if err := client.Connect(ctx); err != nil {
		return policy.Settings{}, err
	}
	defer client.Close()
	creds, err := client.IngestToken(ctx)
	if err != nil {
		return policy.Settings{}, err
	}
	r.ok("token", "collector token for "+creds.IngestURL)
	cache, err := client.Settings(ctx)
	if err != nil {
		return policy.Settings{}, err
	}
	settings, err := policy.Parse(cache.Document, cfg.home)
	if err != nil {
		return policy.Settings{}, fmt.Errorf("the settings: %w", err)
	}
	r.ok("settings", fmt.Sprintf("version %d", cache.Version))
	return settings, nil
}

// applySettings applies the settings to both agents the way the daemon does.
func applySettings(cfg daemonConfig, settings policy.Settings, r *report) apply.Result {
	creds, err := cfg.paths.ReadCredentials()
	if err != nil {
		r.failed("agents", err)
		return apply.Result{}
	}
	result, err := newApplier(cfg).Apply(settings, creds)
	if err != nil {
		r.failed("agents", err)
	}
	if result.Agents == nil {
		return result
	}
	for _, agent := range []policy.Agent{policy.Claude, policy.Codex} {
		res := result.Agents[agent]
		switch res.Status {
		case apply.StatusNotInstalled:
			r.line(string(agent), "not installed", "")
		case apply.StatusOK:
			r.ok(string(agent), "hooks and native OTel")
		default:
			var failures []string
			for _, step := range res.Steps {
				if step.Status != apply.StatusOK {
					failures = append(failures, step.Step+" "+step.Status+stepError(step))
				}
			}
			r.failed(string(agent), errors.New(strings.Join(failures, "; ")))
		}
	}
	return result
}

func stepError(step apply.StepResult) string {
	if step.Error == "" {
		return ""
	}
	return ": " + step.Error
}

// testEvent queues a hook event of the first agent in agent, then the other, whose hooks
// the settings send, and waits for the daemon to send it.
func testEvent(cfg installConfig, settings policy.Settings, agent mcpconfig.Agent, r *report) {
	const item = "test event"
	event, err := json.Marshal(map[string]string{
		"session_id":      testEventSession,
		"hook_event_name": testEventName,
		"cwd":             cfg.daemon.home,
	})
	if err != nil {
		r.failed(item, err)
		return
	}
	q := &putRecorder{Queue: queue.Open(cfg.daemon.paths, 0, nil)}
	// Records already queued go out before the test event.
	before, err := q.Stats()
	if err != nil {
		r.failed(item, err)
		return
	}
	agents := []policy.Agent{policy.Agent(agent), policy.Claude, policy.Codex}
	for _, a := range agents {
		if q.id != "" {
			break
		}
		if err := hook.Handle(settings, a, event, time.Now(), q, cfg.daemon.paths.SkillsRequestsDir()); err != nil {
			r.failed(item, err)
			return
		}
	}
	if q.id == "" {
		r.line(item, "skipped", "the settings send no hook events")
		return
	}
	sent, err := waitSent(cfg, q.id)
	switch {
	case err != nil:
		r.failed(item, err)
	case sent:
		r.ok(item, "sent by the daemon")
	case before.Queued != 0:
		// A backlog the daemon is still sending is no failure of the installation.
		r.line(item, "pending", fmt.Sprintf("not sent within %s behind %d earlier records; it stays queued%s",
			cfg.wait, before.Queued, senderProblem(cfg.daemon.paths)))
	default:
		r.failed(item, fmt.Errorf("not sent within %s%s; it stays queued", cfg.wait, senderProblem(cfg.daemon.paths)))
	}
}

// waitSent waits until the record id leaves the queue, and reports whether the service
// took it rather than rejected it.
func waitSent(cfg installConfig, id string) (bool, error) {
	deadline := time.Now().Add(cfg.wait)
	for {
		_, err := os.Stat(filepath.Join(cfg.daemon.paths.QueueDir(), id))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return false, fmt.Errorf("look at the queue: %w", err)
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(cfg.poll)
	}
	_, err := os.Stat(filepath.Join(cfg.daemon.paths.RejectedDir(), id))
	if err == nil {
		reason, _ := os.ReadFile(filepath.Join(cfg.daemon.paths.RejectedDir(), id+".reason"))
		return false, fmt.Errorf("the service rejected it: %s", strings.TrimSpace(string(reason)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("look at the rejected records: %w", err)
	}
	return true, nil
}

// senderProblem is what the sender last saw go wrong, for the report.
func senderProblem(paths state.Paths) string {
	st, err := sender.ReadStatus(paths.SenderFile())
	switch {
	case err != nil:
		return ""
	case st.Unauthorized != "":
		return ": " + st.Unauthorized
	case st.LastError != nil:
		return ": " + st.LastError.Reason
	default:
		return ""
	}
}

// putRecorder remembers the id of the record put into the queue.
type putRecorder struct {
	*queue.Queue

	id string
}

func (p *putRecorder) Put(kind, payload []byte) (string, error) {
	id, err := p.Queue.Put(kind, payload)
	p.id = id
	return id, err
}

// report prints the outcome of every step.
type report struct {
	out       io.Writer
	anyFailed bool
}

func (r *report) line(item, status, detail string) {
	if detail == "" {
		fmt.Fprintf(r.out, "  %-11s %s\n", item, status)
		return
	}
	fmt.Fprintf(r.out, "  %-11s %-13s %s\n", item, status, detail)
}

func (r *report) ok(item, detail string) { r.line(item, "ok", detail) }

func (r *report) failed(item string, err error) {
	r.anyFailed = true
	r.line(item, "failed", err.Error())
}

// fail reports a step that stops the installation.
func (r *report) fail(stderr io.Writer, item string, err error) int {
	r.failed(item, err)
	fmt.Fprintln(stderr, "hottell install: stopped, see above; run the installation again once it is fixed")
	return exitFailure
}

// manualSteps prints what the user still has to do, by when each agent picks up the
// changes (docs/specs/hottell-contract/apply-timing.md): Claude Code starts native OTel
// with a new session, Codex reads its hooks, their trust and OTel once per process.
func (r *report) manualSteps(result apply.Result) {
	var steps []string
	if s := result.Agents[policy.Claude].Status; s != "" && s != apply.StatusNotInstalled {
		steps = append(steps, "Claude Code: start new sessions; running ones keep sending native OTel the old way. Hooks work at once.")
	}
	codexRes := result.Agents[policy.Codex]
	if codexRes.Status != "" && codexRes.Status != apply.StatusNotInstalled {
		steps = append(steps, "Codex: restart it, including the TUI and Codex Desktop; it reads hooks and native OTel once per process.")
		for _, step := range codexRes.Steps {
			if step.Step == apply.StepTrust && step.Status != apply.StatusOK {
				steps = append(steps, "Codex: the trust of the hottell hooks is not recorded; run /hooks in Codex and trust them.")
			}
		}
	}
	if len(steps) == 0 {
		return
	}
	fmt.Fprintln(r.out, "Left to do:")
	for _, step := range steps {
		fmt.Fprintln(r.out, "  - "+step)
	}
}
