package analytics

import (
	"cmp"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

// RunCounts are the runs of a command, by outcome.
type RunCounts struct {
	Runs   int `json:"runs"`
	Failed int `json:"failed"`
	// Unknown are the runs whose outcome is unknown or done: nothing tells success from failure.
	Unknown int `json:"unknown"`
}

// add counts one run of state.
func (n *RunCounts) add(state string) {
	n.Runs++
	switch state {
	case StateError:
		n.Failed++
	case StateUnknown, StateDone:
		n.Unknown++
	}
}

// CommandRow is one command of a project in the commands aggregate.
type CommandRow struct {
	// Cmd is the command as NormalizeCmd groups it; Project the session's project.
	Cmd     string `json:"cmd"`
	Project string `json:"project"`
	RunCounts
	// P95S is the 95th percentile of the known durations in seconds, nil without one; TotalMin
	// their sum in minutes to two decimals.
	P95S     *float64 `json:"p95_s"`
	TotalMin float64  `json:"total_min"`
	// BySession are the runs by session id; they add up to the row.
	BySession map[string]RunCounts `json:"by_session"`

	durations []float64
}

// commandLabelMax bounds a command label; a normalized command is a program and one word, so
// only a pasted URL or value reaches it.
const commandLabelMax = 200

// AggregateCommands counts the shell commands of the sessions by normalized command and project,
// as the colleague's aggregate_commands. The most run comes first, a tie by command, then by
// project. The label is masked by commandLabel.
func AggregateCommands(sessions []*BuiltSession) []CommandRow {
	type key struct{ cmd, project string }
	rows := map[key]*CommandRow{}
	for _, b := range sessions {
		for i := range b.Calls.Calls {
			c := &b.Calls.Calls[i]
			if c.Cmd == "" {
				continue
			}
			k := key{commandLabel(b.memo, c.Cmd), b.Session.Project}
			r, ok := rows[k]
			if !ok {
				r = &CommandRow{Cmd: k.cmd, Project: k.project, BySession: map[string]RunCounts{}}
				rows[k] = r
			}
			r.add(c.State)
			bs := r.BySession[b.Key.SessionID]
			bs.add(c.State)
			r.BySession[b.Key.SessionID] = bs
			if c.DurationS != nil {
				r.durations = append(r.durations, *c.DurationS)
			}
		}
	}
	out := make([]CommandRow, 0, len(rows))
	for _, r := range rows {
		r.P95S = pctOf(r.durations, 0.95)
		total := 0.0
		for _, d := range r.durations {
			total += d
		}
		r.TotalMin = round2(total / 60)
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b CommandRow) int {
		return cmp.Or(cmp.Compare(b.Runs, a.Runs), cmp.Compare(a.Cmd, b.Cmd), cmp.Compare(a.Project, b.Project))
	})
	return out
}

// commandLabel is the normalized command cmd, masked. Normalizing drops a flag and keeps its
// value, so a word of the label that masking cmd hides is a secret and is masked itself; the
// label is then cleaned, since a positional word may carry a secret of its own. cmd is
// normalized unmasked: masking may eat the separator after a secret and merge two commands.
// memo is the build's; nil masks every time.
func commandLabel(memo *redact.Memo, cmd string) string {
	words := strings.Fields(NormalizeCmd(cmd))
	masked := redactWith(memo, cmd)
	for i, w := range words {
		if i > 0 && !strings.Contains(masked, w) {
			words[i] = redact.Mask
		}
	}
	return cleanWith(memo, strings.Join(words, " "), commandLabelMax)
}
