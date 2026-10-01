package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func lookup(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

//nolint:paralleltest // sets the package-level version the linker would set
func TestRunVersion(t *testing.T) {
	version = "1.2.3"
	t.Cleanup(func() { version = "" })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, nil, &stdout, &stderr, noEnv); code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitOK, stderr.String())
	}
	if got, want := stdout.String(), "1.2.3\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunExitCodes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args      []string
		want      int
		wantUsage bool
	}{
		"no command":      {args: nil, want: exitUsage, wantUsage: true},
		"unknown command": {args: []string{"frobnicate"}, want: exitUsage, wantUsage: true},
		"unknown word":    {args: []string{"foo"}, want: exitUsage, wantUsage: true},
		"hook claude":     {args: []string{"hook", "claude"}, want: exitOK},
		"hook codex":      {args: []string{"hook", "codex"}, want: exitOK},
		// A hook never fails the agent, whatever it is called with.
		"hook without agent": {args: []string{"hook"}, want: exitOK},
		"hook unknown agent": {args: []string{"hook", "cursor"}, want: exitOK},
		// The prototype's hooks call hottell -agent claude|codex; a flag first is always a hook call.
		"legacy -agent claude":         {args: []string{"-agent", "claude"}, want: exitOK},
		"legacy -agent codex":          {args: []string{"-agent", "codex"}, want: exitOK},
		"legacy --agent=claude":        {args: []string{"--agent=claude"}, want: exitOK},
		"legacy -agent=claude":         {args: []string{"-agent=claude"}, want: exitOK},
		"legacy --agent=codex":         {args: []string{"--agent=codex"}, want: exitOK},
		"legacy -config x -agent":      {args: []string{"-config", "x", "-agent", "claude"}, want: exitOK},
		"legacy -agent claude -config": {args: []string{"-agent", "claude", "-config=x"}, want: exitOK},
		"legacy -agent without value":  {args: []string{"-agent"}, want: exitOK},
		"unknown flag":                 {args: []string{"-unknown"}, want: exitOK},
		// Help is the one flag a person types; a hook never passes it.
		"-h":        {args: []string{"-h"}, want: exitOK, wantUsage: true},
		"-help":     {args: []string{"-help"}, want: exitOK, wantUsage: true},
		"--help":    {args: []string{"--help"}, want: exitOK, wantUsage: true},
		"install":   {args: []string{"install"}, want: exitFailure},
		"uninstall": {args: []string{"uninstall"}, want: exitFailure},
		"status":    {args: []string{"status"}, want: exitFailure},
		"daemon":    {args: []string{"daemon"}, want: exitFailure},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"HOTTELL_HOME": t.TempDir()}
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, strings.NewReader("{}"), &stdout, &stderr, lookup(env)); code != tc.want {
				t.Errorf("exit code = %d, want %d", code, tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if got := bytes.Contains(stderr.Bytes(), []byte(usage)); got != tc.wantUsage {
				t.Errorf("usage in stderr = %t, want %t; stderr: %q", got, tc.wantUsage, stderr.String())
			}
		})
	}
}

func TestDevVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		info *debug.BuildInfo
		want string
	}{
		"no build info": {info: nil, want: "dev"},
		"no revision":   {info: &debug.BuildInfo{}, want: "dev"},
		"revision": {
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs", Value: "git"},
				{Key: "vcs.revision", Value: "a7b978c"},
			}},
			want: "dev+a7b978c",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := devVersion(tc.info); got != tc.want {
				t.Errorf("devVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}
