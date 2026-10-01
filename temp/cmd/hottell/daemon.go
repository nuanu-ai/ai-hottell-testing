package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/claude"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/codex"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/apply"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/backup"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/hook"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpconfig"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/otlp"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/sender"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/transcript"
	claudetranscript "git.alva.dev/alva/harness-telemetry/internal/hottell/transcript/claude"
	codextranscript "git.alva.dev/alva/harness-telemetry/internal/hottell/transcript/codex"
)

// Timings of the daemon.
const (
	// scanInterval is how often the transcripts are scanned and the settings applied
	// without a signal.
	scanInterval = 30 * time.Second
	// backfillPoll is how often a backfill with history left looks whether the queue has
	// room for its next pass.
	backfillPoll = sender.PollInterval
	// tokenRetry is the pause between attempts to fetch the collector token after a 401,
	// until one brings a token other than the refused one.
	tokenRetry = time.Minute
	// shutdownGrace bounds the wait for the current send and scan after SIGTERM. A request
	// the service has not answered by then stays in the queue and goes again after the
	// restart, which ingest.md allows. launchd
	// kills the process 20 s after it.
	shutdownGrace = 15 * time.Second
)

// The daemon's log in the logs directory, rotated by size into daemon.log.1.
const (
	daemonLog      = "daemon.log"
	daemonLogBytes = 10 << 20
)

// errAlreadyRunning means another daemon holds the lock.
var errAlreadyRunning = errors.New("another hottell daemon is running")

// stopEvents are the hook events after which the agent has written to its transcript.
//
//nolint:gochecknoglobals // a constant list
var stopEvents = []string{"Stop", "SubagentStop", "SessionEnd"}

// daemonConfig is where the daemon finds the state and the agents, and its timings.
type daemonConfig struct {
	paths state.Paths
	home  string
	// claudeDir is ~/.claude, or CLAUDE_CONFIG_DIR.
	claudeDir string
	codexHome string
	// sources are the agents' configs holding the MCP server.
	sources []mcpconfig.Source
	// binary is the absolute path of this binary, which the hooks run.
	binary  string
	version string
	host    string

	scanEvery  time.Duration
	tokenEvery time.Duration
	grace      time.Duration
}

// runDaemon runs the daemon until SIGTERM or an interrupt.
func runDaemon(args []string, stderr io.Writer, getenv func(string) string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	cfg, err := daemonConfigFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hottell daemon: %v\n", err)
		return exitFailure
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := serve(ctx, cfg); err != nil {
		fmt.Fprintf(stderr, "hottell daemon: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// agentLocations are the home directory and the agents' config locations.
type agentLocations struct {
	home string
	// claudeDir is ~/.claude, or CLAUDE_CONFIG_DIR; claudeJSON is .claude.json beside it.
	claudeDir, claudeJSON string
	// codexHome is ~/.codex, or CODEX_HOME resolved through its symlinks. When CODEX_HOME
	// cannot be resolved, codexHome is CODEX_HOME as it is set and codexErr says why.
	codexHome string
	codexErr  error
}

// agentDirs resolves the home directory and the agents' config locations from the
// environment the way the agents do.
func agentDirs(getenv func(string) string) (agentLocations, error) {
	home := getenv("HOME")
	if home == "" {
		return agentLocations{}, errors.New("HOME is not set")
	}
	loc := agentLocations{home: home, claudeDir: filepath.Join(home, ".claude"), claudeJSON: filepath.Join(home, ".claude.json")}
	if dir := getenv(mcpconfig.ClaudeConfigDirEnv); dir != "" {
		loc.claudeDir, loc.claudeJSON = dir, filepath.Join(dir, ".claude.json")
	}
	loc.codexHome, loc.codexErr = codex.Home(getenv)
	if loc.codexErr != nil {
		loc.codexHome = getenv(mcpconfig.CodexHomeEnv)
	}
	return loc, nil
}

// daemonConfigFrom resolves the locations from the environment the way the agents do. The
// Codex home must resolve: the trust keys of the hooks are built from it.
func daemonConfigFrom(getenv func(string) string) (daemonConfig, error) {
	loc, err := agentDirs(getenv)
	if err != nil {
		return daemonConfig{}, err
	}
	if loc.codexErr != nil {
		return daemonConfig{}, loc.codexErr
	}
	paths, _, err := hookPaths(getenv)
	if err != nil {
		return daemonConfig{}, err
	}
	home, claudeDir, claudeJSON, codexHome := loc.home, loc.claudeDir, loc.claudeJSON, loc.codexHome
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil {
		return daemonConfig{}, fmt.Errorf("resolve the binary path: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return daemonConfig{}, fmt.Errorf("resolve the host name: %w", err)
	}
	return daemonConfig{
		paths:     paths,
		home:      home,
		claudeDir: claudeDir,
		codexHome: codexHome,
		sources: []mcpconfig.Source{
			{Agent: mcpconfig.Claude, Path: claudeJSON},
			{Agent: mcpconfig.Codex, Path: codex.ConfigFile(codexHome)},
		},
		binary:     binary,
		version:    currentVersion(),
		host:       host,
		scanEvery:  scanInterval,
		tokenEvery: tokenRetry,
		grace:      shutdownGrace,
	}, nil
}

// daemon is the background process: it keeps the settings subscription, applies the
// settings to the agents, scans the transcripts and sends the queue.
type daemon struct {
	cfg     daemonConfig
	log     *slog.Logger
	queue   *queue.Queue
	mcp     *mcpclient.Client
	applier apply.Applier
	claude  *claudetranscript.Reader
	codex   *codextranscript.Reader

	// applyNow: the settings or the collector token changed.
	applyNow chan struct{}
	// scanNow: the settings changed, or an agent stopped and wrote its transcript.
	scanNow chan struct{}
	// refresh: the service refused the collector token.
	refresh chan struct{}
	// wake: the queue has new records for the sender.
	wake chan struct{}
}

// serve runs the daemon until ctx ends, then waits for the current send and scan,
// within cfg.grace. It fails at once when another daemon is running.
func serve(ctx context.Context, cfg daemonConfig) error {
	if err := cfg.paths.Ensure(); err != nil {
		return err
	}
	lock, err := lockDaemon(cfg.paths.LockFile())
	if err != nil {
		return err
	}
	defer lock.Close()
	logFile, err := openRotating(filepath.Join(cfg.paths.Logs, daemonLog), daemonLogBytes)
	if err != nil {
		return err
	}
	defer logFile.Close()

	d := newDaemon(cfg, slog.New(slog.NewTextHandler(logFile, nil)))
	d.log.InfoContext(ctx, "the daemon starts", "version", cfg.version)
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){d.watch, d.applyLoop, d.tokenLoop, d.send, d.scanLoop} {
		wg.Go(func() { loop(ctx) })
	}
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()

	<-ctx.Done()
	d.log.InfoContext(ctx, "the daemon stops")
	select {
	case <-stopped:
		d.log.InfoContext(ctx, "the daemon stopped")
	case <-time.After(cfg.grace):
		d.log.WarnContext(ctx, "the daemon stopped without waiting for the current send or scan", "waited", cfg.grace)
	}
	return nil
}

func newDaemon(cfg daemonConfig, log *slog.Logger) *daemon {
	q := queue.Open(cfg.paths, 0, log)
	offsets := transcript.NewOffsets(cfg.paths.OffsetsDir())
	return &daemon{
		cfg:   cfg,
		log:   log,
		queue: q,
		mcp: mcpclient.New(mcpclient.Config{
			Paths: cfg.paths, Sources: cfg.sources, Version: cfg.version, Log: log,
		}),
		applier:  newApplier(cfg),
		claude:   claudetranscript.New(q, offsets, log),
		codex:    codextranscript.New(q, offsets, log),
		applyNow: make(chan struct{}, 1),
		scanNow:  make(chan struct{}, 1),
		refresh:  make(chan struct{}, 1),
		wake:     make(chan struct{}, 1),
	}
}

// newApplier applies the settings to the agents found by cfg, with hooks that run
// cfg.binary.
func newApplier(cfg daemonConfig) apply.Applier {
	short, _, _ := strings.Cut(cfg.host, ".")
	return apply.Applier{
		ClaudeDir:  cfg.claudeDir,
		CodexHome:  cfg.codexHome,
		BinaryPath: cfg.binary,
		Paths:      cfg.paths,
		Resource:   claude.Resource{Host: short, Version: cfg.version},
		Backup:     agentBackups(cfg),
	}
}

// agentBackups keeps copies of the agents' config files hottell writes. ~/.claude.json is
// not among them: Claude Code rewrites it all the time, and putting it back whole would
// undo the agent's own state. hottell only reads the MCP server there and writes one entry,
// hottell-local, which uninstall takes out.
func agentBackups(cfg daemonConfig) *backup.Store {
	return &backup.Store{
		Dir: cfg.paths.BackupDir(),
		Files: []backup.File{
			{Name: "claude-settings.json", Path: filepath.Join(cfg.claudeDir, "settings.json")},
			{Name: "codex-config.toml", Path: codex.ConfigFile(cfg.codexHome)},
			{Name: "codex-hooks.json", Path: codex.HooksFile(cfg.codexHome)},
		},
	}
}

// notify signals ch without blocking; a signal already pending covers this one.
func notify(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// watch keeps the settings subscription; a new version is applied and scanned with.
func (d *daemon) watch(ctx context.Context) {
	_ = d.mcp.Watch(ctx, func(cache state.SettingsCache) {
		d.log.InfoContext(ctx, "new settings", "version", cache.Version)
		notify(d.applyNow)
		notify(d.scanNow)
	})
}

// applyLoop applies the cached settings and credentials to the agents at the start, on
// every change and every scan interval, which also catches a token the subscription
// fetched on a reconnect and an agent installed later. Apply writes nothing when the
// agents' files already hold what it wants.
func (d *daemon) applyLoop(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.scanEvery)
	defer ticker.Stop()
	var last string
	for {
		last = d.apply(last)
		select {
		case <-ctx.Done():
			return
		case <-d.applyNow:
		case <-ticker.C:
		}
	}
}

// apply applies once and logs the outcome when it differs from last, which it returns.
func (d *daemon) apply(last string) string {
	settings, ok := d.settings()
	if !ok {
		return last
	}
	creds, err := d.cfg.paths.ReadCredentials()
	if err != nil {
		d.log.Error("read the credentials", "err", err)
		return last
	}
	if creds.IngestURL == "" || creds.CollectorToken == "" {
		return last
	}
	result, err := d.applier.Apply(settings, creds)
	if err != nil {
		d.log.Error("apply the settings", "err", err)
	}
	if result.Agents == nil {
		// The backup failed, and nothing was applied.
		return last
	}
	outcome := fmt.Sprintf("version %d: %v", result.Version, result.Err())
	if outcome != last {
		if err := result.Err(); err != nil {
			d.log.Warn("settings applied with failures", "version", result.Version, "err", err)
		} else {
			d.log.Info("settings applied", "version", result.Version)
		}
	}
	return outcome
}

// settings returns the cached settings, and false when none are cached: the denials are
// unknown, so nothing is applied or read.
func (d *daemon) settings() (policy.Settings, bool) {
	cache, err := d.cfg.paths.ReadSettings()
	if err != nil {
		d.log.Error("read the settings cache", "err", err)
		return policy.Settings{}, false
	}
	if cache.Document == nil {
		return policy.Settings{}, false
	}
	settings, err := policy.Parse(cache.Document, d.cfg.home)
	if err != nil {
		d.log.Error("parse the settings", "err", err)
		return policy.Settings{}, false
	}
	return settings, true
}

// tokenLoop fetches the collector token again when the service refused it: the user may
// have reissued it in the interface. The sender stops until the saved token changes, so
// the fetch is repeated until it brings another token; that one is applied, and the
// sender resumes as it reads it.
func (d *daemon) tokenLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.refresh:
		}
		// The sender read the refused token from the saved credentials.
		refused, err := d.cfg.paths.ReadCredentials()
		if err != nil {
			d.log.ErrorContext(ctx, "read the credentials", "err", err)
		}
		for {
			creds, err := d.mcp.IngestToken(ctx)
			switch {
			case err != nil:
				d.log.WarnContext(ctx, "fetch the collector token", "err", err, "retry_in", d.cfg.tokenEvery)
			case creds.CollectorToken == refused.CollectorToken:
				d.log.WarnContext(ctx, "the service still gives the refused collector token", "retry_in", d.cfg.tokenEvery)
			default:
				d.log.InfoContext(ctx, "a new collector token", "ingest_url", creds.IngestURL)
				notify(d.applyNow)
				notify(d.wake)
			}
			if err == nil && creds.CollectorToken != refused.CollectorToken {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(d.cfg.tokenEvery):
			}
		}
	}
}

// send runs the sender; a stop event it reads from the queue triggers a scan.
func (d *daemon) send(ctx context.Context) {
	s := sender.New(sender.Config{
		Queue:          stopWatcher{Queue: d.queue, onStop: func() { notify(d.scanNow) }},
		Credentials:    d.cfg.paths.ReadCredentials,
		Resource:       otlp.Resource{Version: d.cfg.version, HostName: d.cfg.host},
		Wake:           d.wake,
		OnUnauthorized: func(string) { notify(d.refresh) },
		StatusFile:     d.cfg.paths.SenderFile(),
		Log:            d.log,
	})
	_ = s.Run(ctx)
}

// scanLoop scans the transcripts every scan interval and on a signal, and backfills the
// history while the settings ask for it. A scan in progress when ctx ends is finished,
// so that its read positions are saved.
func (d *daemon) scanLoop(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.scanEvery)
	defer ticker.Stop()
	historyLeft := false
	for {
		historyLeft = d.scan(historyLeft)
		var poll <-chan time.Time
		if historyLeft {
			poll = time.After(backfillPoll)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.scanNow:
		case <-poll:
		}
	}
}

// scan queues the new lines of both agents' transcripts, then one backfill pass once the
// queue has room for it, and reports whether history is left.
func (d *daemon) scan(historyLeft bool) bool {
	settings, ok := d.settings()
	if !ok {
		return false
	}
	if err := d.claude.Scan(filepath.Join(d.cfg.claudeDir, "projects"), settings); err != nil {
		d.log.Warn("scan the Claude Code transcripts", "err", err)
	}
	if err := d.codex.Scan(d.cfg.codexHome, settings); err != nil {
		d.log.Warn("scan the Codex transcripts", "err", err)
	}
	defer notify(d.wake)
	if !settings.BackfillHistory {
		return false
	}
	// The history waits for the queue to drain, so that it neither crowds out the live
	// records nor gets them evicted.
	stats, err := d.queue.Stats()
	if err != nil {
		d.log.Error("read the queue size", "err", err)
		return historyLeft
	}
	if stats.QueuedBytes >= transcript.PassBytes {
		return true
	}
	claudeLeft, err := d.claude.Backfill(filepath.Join(d.cfg.claudeDir, "projects"), settings, transcript.PassBytes)
	if err != nil {
		d.log.Warn("backfill the Claude Code history", "err", err)
	}
	codexLeft, err := d.codex.Backfill(d.cfg.codexHome, settings, transcript.PassBytes)
	if err != nil {
		d.log.Warn("backfill the Codex history", "err", err)
	}
	return claudeLeft || codexLeft
}

// stopWatcher is the queue as the sender sees it; it calls onStop when a batch holds a
// hook event after which the agent has written its transcript.
type stopWatcher struct {
	*queue.Queue

	onStop func()
}

func (w stopWatcher) Next(maxBytes int64) ([]queue.Record, error) {
	records, err := w.Queue.Next(maxBytes)
	if slices.ContainsFunc(records, isStopEvent) {
		w.onStop()
	}
	return records, err
}

func isStopEvent(rec queue.Record) bool {
	var meta hook.Meta
	if json.Unmarshal(rec.Kind, &meta) != nil || meta.Kind != hook.KindHook {
		return false
	}
	var event struct {
		HookEventName string `json:"hook_event_name"`
	}
	return json.Unmarshal(rec.Payload, &event) == nil && slices.Contains(stopEvents, event.HookEventName)
}
