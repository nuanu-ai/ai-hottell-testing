package hookcmd_test

import (
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/agentconfig/hookcmd"
)

func TestRuns(t *testing.T) {
	t.Parallel()

	const (
		home   = "/Users/me"
		binary = "/Users/me/.local/bin/hottell"
	)
	for name, tc := range map[string]struct {
		command string
		want    bool
	}{
		"bare":                               {binary + " -agent claude", true},
		"alone":                              {binary, true},
		"leading spaces":                     {"  " + binary + "\t-agent codex -config x", true},
		"single-quoted":                      {"'" + binary + "' -agent claude", true},
		"double-quoted":                      {`"` + binary + `" -agent claude`, true},
		"partly quoted":                      {"/Users/me/.local/bin/'hott'ell -agent claude", true},
		"escaped":                            {`/Users/me/.local/bin/hott\ell -agent claude`, true},
		"tilde":                              {"~/.local/bin/hottell -agent claude", true},
		"HOME":                               {"$HOME/.local/bin/hottell -agent claude", true},
		"braced HOME":                        {"${HOME}/.local/bin/hottell -agent claude", true},
		"then an operator":                   {binary + "; echo done", true},
		"piped":                              {binary + "|cat", true},
		"redirected":                         {binary + ">/dev/null", true},
		"unclean path":                       {"/Users/me/.local//bin/./hottell -agent claude", true},
		"mentioned later":                    {"echo " + binary + " -agent claude", false},
		"run by a shell":                     {"sh -c '" + binary + " -agent claude'", false},
		"env first":                          {"env X=1 " + binary + " -agent claude", false},
		"assignment first":                   {"X=1 " + binary + " -agent claude", false},
		"other binary":                       {"/usr/local/bin/hottell -agent claude", false},
		"longer word":                        {binary + "2 -agent claude", false},
		"quoted tilde":                       {"'~/.local/bin/hottell' -agent claude", false},
		"other variable":                     {"$BIN/hottell -agent claude", false},
		"command substitution":               {"$(which hottell) -agent claude", false},
		"unterminated quote":                 {"'" + binary + " -agent claude", false},
		"empty":                              {"", false},
		"Orca-like long shell hook":          {`if [ -n "$ORCA_PANE" ]; then curl -s localhost:9 -d @- ; fi; ` + binary, false},
		"relative":                           {".local/bin/hottell -agent claude", false},
		"tilde of another user":              {"~other/.local/bin/hottell", false},
		"double-quoted HOME":                 {`"$HOME/.local/bin/hottell" -agent claude`, true},
		"HOME as a prefix":                   {"$HOMEDIR/.local/bin/hottell", false},
		"line continuation":                  {"/Users/me/.local/bin/hott\\\nell -agent claude", true},
		"continued before the word":          {"\\\n" + binary + " -agent claude", true},
		"continued after the word":           {binary + "\\\n -agent claude", true},
		"continuation in tilde":              {"~\\\n/.local/bin/hottell", true},
		"continued in double quotes":         {`"/Users/me/.local/bin/hott\` + "\n" + `ell" -agent claude`, true},
		"backslash-newline in single quotes": {`'/Users/me/.local/bin/hott\` + "\n" + `ell' -agent claude`, false},
		"escaped backslash, then newline":    {binary + "\\\\\n -agent claude", false},
		"dot-dot segment":                    {"/Users/me/.local/bin/x/../hottell -agent claude", false},
		"dot-dot after HOME":                 {"$HOME/../me/.local/bin/hottell -agent claude", false},
		"assignment of a path":               {"X=" + binary + " -agent claude", false},
	} {
		if got := hookcmd.Runs(tc.command, binary, home); got != tc.want {
			t.Errorf("%s: Runs(%q) = %v, want %v", name, tc.command, got, tc.want)
		}
	}
}

// TestRunsHomeWithBlank: an unquoted $HOME is split into words at the blanks of a home
// that has them, so only a quoted one, or ~, which is not split, runs the binary.
func TestRunsHomeWithBlank(t *testing.T) {
	t.Parallel()

	const (
		home   = "/Users/my me"
		binary = "/Users/my me/.local/bin/hottell"
	)
	for name, tc := range map[string]struct {
		command string
		want    bool
	}{
		"unquoted HOME":        {"$HOME/.local/bin/hottell -agent claude", false},
		"unquoted braced HOME": {"${HOME}/.local/bin/hottell -agent claude", false},
		"double-quoted HOME":   {`"$HOME/.local/bin/hottell" -agent claude`, true},
		"partly quoted HOME":   {`"${HOME}"/.local/bin/hottell -agent claude`, true},
		"tilde":                {"~/.local/bin/hottell -agent claude", true},
		"escaped blank":        {`/Users/my\ me/.local/bin/hottell -agent claude`, true},
	} {
		if got := hookcmd.Runs(tc.command, binary, home); got != tc.want {
			t.Errorf("%s: Runs(%q) = %v, want %v", name, tc.command, got, tc.want)
		}
	}
	for _, blank := range []string{"\t", "\n"} {
		home := "/Users/my" + blank + "me"
		if hookcmd.Runs("$HOME/.local/bin/hottell", home+"/.local/bin/hottell", home) {
			t.Errorf("Runs(unquoted $HOME) = true with HOME %q", home)
		}
	}
}

// TestRunsEqualsInPath: a first word is an assignment only when what comes before its
// first = is a name; any other word with = is the command.
func TestRunsEqualsInPath(t *testing.T) {
	t.Parallel()

	const binary = "/opt/k=v/bin/hottell"
	for name, tc := range map[string]struct {
		command string
		want    bool
	}{
		"bare":        {binary + " -agent claude", true},
		"quoted":      {`"` + binary + `" -agent claude`, true},
		"assignment":  {"K=v " + binary, false},
		"quoted name": {`"/opt/k"=v/bin/hottell`, true},
	} {
		if got := hookcmd.Runs(tc.command, binary, "/Users/me"); got != tc.want {
			t.Errorf("%s: Runs(%q) = %v, want %v", name, tc.command, got, tc.want)
		}
	}
}
