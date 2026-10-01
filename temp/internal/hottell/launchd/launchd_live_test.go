package launchd_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/launchd"
)

// TestLive loads the agent into the real gui domain of the current user, so it runs only
// when HOTTELL_LAUNCHD_LIVE=1. The plist, the logs and the stand-in binary live in a
// temporary home; ~/Library/LaunchAgents is not touched.
//
//nolint:paralleltest // owns the agent's label in the user's launchd domain
func TestLive(t *testing.T) {
	if os.Getenv("HOTTELL_LAUNCHD_LIVE") != "1" {
		t.Skip("set HOTTELL_LAUNCHD_LIVE=1 to load the agent into the real launchd")
	}

	home := t.TempDir()
	binary := filepath.Join(home, "hottell")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 3600\n"), 0o700); err != nil { //nolint:gosec // an executable stand-in
		t.Fatal(err)
	}
	agent := launchd.Agent{Home: home, Binary: binary, UID: os.Getuid(), Runner: launchd.ExecRunner{}}

	if st, err := agent.Status(t.Context()); err != nil || st.Loaded {
		t.Fatalf("agent %s is already loaded or unreadable (%+v, %v): refusing to replace it", launchd.Label, st, err)
	}
	if err := agent.Install(t.Context()); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	t.Cleanup(func() { _ = agent.Uninstall(t.Context()) })

	first := waitForPID(t, agent, 0)
	t.Logf("running, pid %d", first)

	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatalf("kill %d: %v", first, err)
	}
	second := waitForPID(t, agent, first)
	t.Logf("restarted after kill, pid %d", second)

	if err := agent.Uninstall(t.Context()); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	if st, err := agent.Status(t.Context()); err != nil || st.Loaded {
		t.Errorf("after Uninstall: status %+v, %v; want not loaded", st, err)
	}
	if err := syscall.Kill(second, 0); err == nil {
		t.Errorf("process %d still alive after Uninstall", second)
	}
	if _, err := os.Stat(agent.PlistPath()); !os.IsNotExist(err) {
		t.Errorf("plist still present: %v", err)
	}
}

// waitForPID waits until the agent runs with a pid other than old.
func waitForPID(t *testing.T, agent launchd.Agent, old int) int {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := agent.Status(t.Context())
		if err != nil {
			t.Fatalf("Status() = %v", err)
		}
		if st.PID != 0 && st.PID != old {
			return st.PID
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("agent did not start a process other than pid %d within 30s", old)
	return 0
}
