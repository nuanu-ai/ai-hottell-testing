package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/apply"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/sender"
)

// launchdTimeout bounds the calls to launchctl.
const launchdTimeout = 10 * time.Second

// statusReport is what hottell status shows, and its JSON with --json. It never holds the
// MCP key or the collector token.
type statusReport struct {
	OK       bool           `json:"ok"`
	Version  string         `json:"version"`
	MCP      mcpStatus      `json:"mcp"`
	Token    tokenStatus    `json:"token"`
	Settings settingsStatus `json:"settings"`
	// Agents holds claude and codex.
	Agents map[policy.Agent]agentStatus `json:"agents"`
	Queue  queueStatus                  `json:"queue"`
	Daemon daemonStatus                 `json:"daemon"`
	// Problems are what makes the status not ok, one line each.
	Problems []string `json:"problems"`
}

type mcpStatus struct {
	// Agent is claude or codex, whose config holds the MCP server; "" when none does.
	Agent       mcpconfig.Agent   `json:"agent,omitempty"`
	Config      string            `json:"config,omitempty"`
	URL         string            `json:"url,omitempty"`
	LastSuccess time.Time         `json:"last_success,omitzero"`
	Error       *mcpclient.Reason `json:"error,omitempty"`
	// Message says what the error is and what to do about it.
	Message string `json:"message,omitempty"`
}

type tokenStatus struct {
	Present   bool      `json:"present"`
	IngestURL string    `json:"ingest_url,omitempty"`
	LastSent  time.Time `json:"last_sent,omitzero"`
	// LastError is the last send the service did not take, until a send succeeds.
	LastError *sender.Failure `json:"last_error,omitempty"`
	// Unauthorized is why sending stopped on 401.
	Unauthorized string `json:"unauthorized,omitempty"`
}

type settingsStatus struct {
	Cached  bool  `json:"cached"`
	Version int64 `json:"version"`
	// Applied is the version last applied to the agents; 0 before the first application.
	Applied int64 `json:"applied"`
}

type agentStatus struct {
	// Status is apply's status of the agent, or "unknown" before the first application.
	Status string `json:"status"`
	// Hooks, Trust (Codex only) and OTel are the statuses of the steps.
	Hooks string `json:"hooks,omitempty"`
	Trust string `json:"trust,omitempty"`
	OTel  string `json:"otel,omitempty"`
	// Errors are the errors of the failed steps.
	Errors []string `json:"errors,omitempty"`
}

type queueStatus struct {
	Queued        int   `json:"queued"`
	QueuedBytes   int64 `json:"queued_bytes"`
	Rejected      int   `json:"rejected"`
	RejectedBytes int64 `json:"rejected_bytes"`
}

type daemonStatus struct {
	Loaded bool `json:"loaded"`
	PID    int  `json:"pid,omitempty"`
	// LastExitCode is the code of the last exit, when the daemon has exited.
	LastExitCode *int `json:"last_exit_code,omitempty"`
}

// statusUnknown is the status of an agent before the first application of the settings.
const statusUnknown = "unknown"

// runStatus prints the status and exits 0 when everything works, 1 otherwise.
func runStatus(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	asJSON := false
	switch {
	case len(args) == 1 && args[0] == "--json":
		asJSON = true
	case len(args) != 0:
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	cfg, err := installConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell status: %v\n", err)
		return exitFailure
	}
	rep := collectStatus(context.Background(), cfg)
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(stderr, "hottell status: %v\n", err)
			return exitFailure
		}
	} else {
		printStatus(stdout, rep)
	}
	if !rep.OK {
		return exitFailure
	}
	return exitOK
}

// collectStatus reads what the daemon, install and the hooks left in the state and the
// hooks in the agents' configs, and asks launchd about the daemon. It goes to no other
// network and changes nothing.
func collectStatus(ctx context.Context, cfg installConfig) statusReport {
	rep := statusReport{Version: currentVersion(), Agents: map[policy.Agent]agentStatus{}}
	paths := cfg.daemon.paths
	problem := func(format string, args ...any) {
		rep.Problems = append(rep.Problems, fmt.Sprintf(format, args...))
	}

	// MCP: the config that holds the server now, and what the client last saw of it; what
	// it saw of another config, or of the same one at another URL, no longer applies.
	mcpSt, err := mcpclient.ReadStatus(paths.MCPFile())
	if err != nil {
		problem("mcp: %v", err)
	}
	if url, _, src, err := mcpconfig.FindIn(cfg.daemon.sources...); err != nil {
		reason := mcpclient.ReasonNoServer
		rep.MCP.Error = &reason
		rep.MCP.Message = (&mcpclient.Problem{Reason: reason, Detail: err.Error()}).Message()
	} else {
		rep.MCP = mcpStatus{Agent: src.Agent, Config: src.Path, URL: url}
		if mcpSt.Source == src && mcpSt.URL == url {
			rep.MCP.LastSuccess = mcpSt.LastSuccess
			if mcpSt.Problem != nil {
				rep.MCP.Error, rep.MCP.Message = &mcpSt.Problem.Reason, mcpSt.Problem.Message()
			}
		}
	}
	switch {
	case rep.MCP.Error != nil:
		problem("mcp: %s", rep.MCP.Message)
	case rep.MCP.LastSuccess.IsZero():
		problem("mcp: the service has not answered yet")
	}

	creds, err := paths.ReadCredentials()
	if err != nil {
		problem("token: %v", err)
	}
	rep.Token = tokenStatus{Present: creds.CollectorToken != "", IngestURL: creds.IngestURL}
	if !rep.Token.Present {
		problem("token: no collector token")
	}
	sendSt, err := sender.ReadStatus(paths.SenderFile())
	if err != nil {
		problem("token: %v", err)
	}
	rep.Token.LastSent, rep.Token.LastError, rep.Token.Unauthorized = sendSt.LastSuccess, sendSt.LastError, sendSt.Unauthorized
	switch {
	case sendSt.Unauthorized != "":
		problem("token: sending stopped: %s", sendSt.Unauthorized)
	case sendSt.LastError != nil:
		problem("token: the last send failed: %s", sendSt.LastError.Reason)
	}

	cache, err := paths.ReadSettings()
	if err != nil {
		problem("settings: %v", err)
	}
	rep.Settings = settingsStatus{Cached: cache.Document != nil, Version: cache.Version}
	if !rep.Settings.Cached {
		problem("settings: no settings cached")
	}

	result, applied, err := apply.ReadResult(paths)
	if err != nil {
		problem("agents: %v", err)
	}
	rep.Settings.Applied = result.Version
	for _, agent := range []policy.Agent{policy.Claude, policy.Codex} {
		st := agentStatusOf(result.Agents[agent], applied)
		rep.Agents[agent] = st
		switch st.Status {
		case apply.StatusOK, apply.StatusNotInstalled:
		case statusUnknown:
			problem("%s: the settings have not been applied yet", agent)
		default:
			problem("%s: %s", agent, strings.Join(st.Errors, "; "))
		}
	}
	foreign, err := foreignHooks(cfg.daemon)
	if err != nil {
		problem("hooks: %v", err)
	}
	for _, h := range foreign {
		problem("hooks: %s %s runs the hottell binary as a foreign hook: %s", h.file, h.Event, h.Command)
	}
	if applied && rep.Settings.Cached && result.Version != cache.Version {
		problem("settings: version %d is cached, version %d is applied", cache.Version, result.Version)
	}

	stats, err := queue.Open(paths, 0, nil).Stats()
	if err != nil {
		problem("queue: %v", err)
	}
	rep.Queue = queueStatus(stats)

	ctx, cancel := context.WithTimeout(ctx, launchdTimeout)
	defer cancel()
	agent := launchd.Agent{Home: cfg.daemon.home, Binary: cfg.daemon.binary, UID: cfg.uid, Runner: cfg.launchd}
	ld, err := agent.Status(ctx)
	if err != nil {
		problem("daemon: %v", err)
	}
	rep.Daemon = daemonStatus{Loaded: ld.Loaded, PID: ld.PID}
	if ld.Exited {
		rep.Daemon.LastExitCode = &ld.LastExitCode
	}
	switch {
	case err != nil:
	case !ld.Loaded:
		problem("daemon: not loaded into launchd")
	case ld.PID == 0:
		problem("daemon: loaded but not running")
	}

	rep.OK = len(rep.Problems) == 0
	if rep.Problems == nil {
		rep.Problems = []string{}
	}
	return rep
}

// agentStatusOf turns apply's outcome of one agent into its status.
func agentStatusOf(res apply.AgentResult, applied bool) agentStatus {
	if !applied || res.Status == "" {
		return agentStatus{Status: statusUnknown}
	}
	st := agentStatus{Status: res.Status}
	for _, step := range res.Steps {
		switch step.Step {
		case apply.StepHooks:
			st.Hooks = step.Status
		case apply.StepTrust:
			st.Trust = step.Status
		case apply.StepOTel:
			st.OTel = step.Status
		}
		if step.Status != apply.StatusOK {
			st.Errors = append(st.Errors, step.Step+" "+step.Status+stepError(step))
		}
	}
	return st
}

// printStatus prints the status for a person, in the layout of install's report.
func printStatus(out io.Writer, rep statusReport) {
	r := &report{out: out}
	r.line("version", rep.Version, "")

	switch {
	case rep.MCP.Error != nil:
		r.line("mcp", string(*rep.MCP.Error), rep.MCP.Message)
	case rep.MCP.LastSuccess.IsZero():
		r.line("mcp", "no answer", fmt.Sprintf("%s config %s, %s", rep.MCP.Agent, rep.MCP.Config, rep.MCP.URL))
	default:
		r.line("mcp", "ok", fmt.Sprintf("%s config %s, %s, last answer %s",
			rep.MCP.Agent, rep.MCP.Config, rep.MCP.URL, when(rep.MCP.LastSuccess)))
	}

	switch {
	case !rep.Token.Present:
		r.line("token", "missing", "")
	case rep.Token.Unauthorized != "":
		r.line("token", "refused", rep.Token.Unauthorized)
	default:
		detail := "for " + rep.Token.IngestURL + ", last sent " + when(rep.Token.LastSent)
		if rep.Token.LastError != nil {
			r.line("token", "send failed", fmt.Sprintf("%s at %s; %s",
				rep.Token.LastError.Reason, when(rep.Token.LastError.At), detail))
		} else {
			r.line("token", "ok", detail)
		}
	}

	if rep.Settings.Cached {
		r.line("settings", "ok", fmt.Sprintf("version %d, applied %d", rep.Settings.Version, rep.Settings.Applied))
	} else {
		r.line("settings", "missing", "")
	}

	for _, agent := range []policy.Agent{policy.Claude, policy.Codex} {
		st := rep.Agents[agent]
		switch st.Status {
		case apply.StatusOK, apply.StatusFailed:
			steps := []string{"hooks " + st.Hooks}
			if st.Trust != "" {
				steps = append(steps, "trust "+st.Trust)
			}
			steps = append(steps, "otel "+st.OTel)
			detail := strings.Join(steps, ", ")
			if len(st.Errors) != 0 {
				detail += ": " + strings.Join(st.Errors, "; ")
			}
			r.line(string(agent), st.Status, detail)
		default:
			r.line(string(agent), strings.ReplaceAll(st.Status, "_", " "), "")
		}
	}

	r.line("queue", fmt.Sprintf("%d records", rep.Queue.Queued), fmt.Sprintf("%d bytes; rejected %d records, %d bytes",
		rep.Queue.QueuedBytes, rep.Queue.Rejected, rep.Queue.RejectedBytes))

	switch {
	case rep.Daemon.PID != 0:
		r.line("daemon", "running", fmt.Sprintf("pid %d", rep.Daemon.PID))
	case rep.Daemon.Loaded:
		detail := ""
		if rep.Daemon.LastExitCode != nil {
			detail = fmt.Sprintf("last exit code %d", *rep.Daemon.LastExitCode)
		}
		r.line("daemon", "not running", detail)
	default:
		r.line("daemon", "not loaded", "")
	}

	if rep.OK {
		fmt.Fprintln(out, "Everything works.")
		return
	}
	fmt.Fprintln(out, "Problems:")
	for _, p := range rep.Problems {
		fmt.Fprintln(out, "  - "+p)
	}
}

// when is t in the local time, or never.
func when(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format(time.DateTime)
}
