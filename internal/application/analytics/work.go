package analytics

import (
	"regexp"
	"slices"
	"strconv"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// Claude Code's metrics of what a session delivered. Their type is "" for the counts and
// added or removed for the lines.
const (
	commitMetric      = "claude_code.commit.count"
	pullRequestMetric = "claude_code.pull_request.count"
	linesMetric       = "claude_code.lines_of_code.count"
)

var (
	// gitCommitOutRe is the "[branch sha]" line git commit prints on success. Claude's
	// tool_response is a JSON object, so the line may also start a JSON stdout string or follow
	// an escaped \n inside it.
	gitCommitOutRe = regexp.MustCompile(`(?m)(?:^|\\n|"stdout":")\[[^\]\n]+ [0-9a-f]{7,40}\]`)
	// prURLRe is the address of the pull request gh pr create prints on success.
	prURLRe = regexp.MustCompile(`https://github\.com/[^\s/]+/[^\s/]+/pull/\d+`)
	// commitStatRe is git's "N files changed, A insertions(+), D deletions(-)".
	commitStatRe = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)
)

// Work is what a session delivered: its commits, pull requests and the lines they changed.
type Work struct {
	Commits int
	PRs     int
	// Added and Removed are the lines the session's commits added and removed; nil while unknown.
	Added   *int64
	Removed *int64
	// Unconfirmed counts the git commit and gh pr create calls of the hooks whose output carries
	// no sign of success; they are not counted.
	Unconfirmed int
}

// SessionWork is what a session delivered. Claude with metrics counts commit.count,
// pull_request.count and lines_of_code.count; the lines are null without a lines_of_code point.
// Otherwise the hooks count: a git commit is one when its output has the "[branch sha]" line,
// with the lines of git's stat line; a gh pr create when its output has the pull request's
// address; a call that failed or lacks the sign is unconfirmed. The lines are the sum of the
// commits' stats when every commit has one; a recorded Codex session without commits and
// unconfirmed calls changed 0 lines; otherwise, and in a partial session, they are unknown.
func SessionWork(agent string, calls []Call, metrics []telemetry.ClaudeMetric, partial bool) Work {
	hooks, stats := hookWork(calls)
	if agent == "claude" && len(metrics) > 0 {
		return claudeWork(metrics)
	}
	w := hooks
	switch {
	case len(stats) > 0 && !slices.Contains(stats, nil):
		var added, removed int64
		for _, s := range stats {
			added += s[0]
			removed += s[1]
		}
		w.Added, w.Removed = &added, &removed
	case len(stats) == 0 && !partial && agent == "codex" && w.Unconfirmed == 0:
		var added, removed int64
		w.Added, w.Removed = &added, &removed
	}
	return w
}

// claudeWork reads Claude Code's metrics of commits, pull requests and lines.
func claudeWork(metrics []telemetry.ClaudeMetric) Work {
	var commits, prs, added, removed float64
	hasLines := false
	for _, m := range metrics {
		switch {
		case m.Metric == commitMetric && m.Type == "":
			commits += m.Value
		case m.Metric == pullRequestMetric && m.Type == "":
			prs += m.Value
		case m.Metric == linesMetric && m.Type == "added":
			added += m.Value
			hasLines = true
		case m.Metric == linesMetric && m.Type == "removed":
			removed += m.Value
			hasLines = true
		}
	}
	w := Work{Commits: int(commits), PRs: int(prs)}
	if hasLines {
		a, r := int64(added), int64(removed)
		w.Added, w.Removed = &a, &r
	}
	return w
}

// hookWork counts the confirmed git commit and gh pr create calls of a session's shell commands,
// and the added and removed lines of each confirmed commit, nil for one without a stat line.
func hookWork(calls []Call) (Work, []*[2]int64) {
	var w Work
	var stats []*[2]int64
	for i := range calls {
		c := &calls[i]
		if c.Cmd == "" {
			continue
		}
		isCommit, isPR := false, false
		for _, seg := range SplitSegments(c.Cmd) {
			seg = StripPrefix(seg)
			if len(seg) == 0 {
				continue
			}
			pos := Positional(seg)
			switch pyBasename(seg[0]) {
			case "git":
				isCommit = isCommit || len(pos) > 0 && pos[0] == "commit"
			case "gh":
				isPR = isPR || len(pos) > 1 && pos[0] == "pr" && pos[1] == "create"
			}
		}
		out := c.RespHead + "\n" + c.RespTail
		ok := c.State != StateError
		if isCommit {
			if ok && gitCommitOutRe.MatchString(out) {
				w.Commits++
				stats = append(stats, commitStat(out))
			} else {
				w.Unconfirmed++
			}
		}
		if isPR {
			if ok && prURLRe.MatchString(out) {
				w.PRs++
			} else {
				w.Unconfirmed++
			}
		}
	}
	return w, stats
}

// commitStat is the added and removed lines of git's stat line in out, nil without one.
func commitStat(out string) *[2]int64 {
	m := commitStatRe.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	added, _ := strconv.ParseInt(m[2], 10, 64)
	removed, _ := strconv.ParseInt(m[3], 10, 64)
	return &[2]int64{added, removed}
}

// WorkGap is the line of the gaps for a session's unconfirmed git commit and gh pr create
// calls; false when it has none.
func WorkGap(sid string, w Work) (string, bool) {
	if w.Unconfirmed == 0 {
		return "", false
	}
	return ShortID(sid) + ": " + strconv.Itoa(w.Unconfirmed) +
		" git commit / gh pr create без признака успеха в выводе — не посчитаны.", true
}

// ActiveMinutes is the agent's active time in a session, in minutes to one decimal: Claude
// Code's claude_code.active_time.total of type cli when the session has it, else the length of
// its turns (TurnMinutes).
func ActiveMinutes(turns []*Turn, metrics []telemetry.ClaudeMetric) float64 {
	seconds, ok := 0.0, false
	for _, m := range metrics {
		if m.Metric == activeTimeMetric && m.Type == "cli" {
			seconds += m.Value
			ok = true
		}
	}
	if ok {
		return roundTo(seconds/60, 1)
	}
	return TurnMinutes(turns)
}

// AgentMinutesByDay is the agent's active time by day of the zone loc, oldest first, unrounded:
// the active_time.total points of type cli on the day of their time when the session has any,
// else its turns split at midnight.
func AgentMinutesByDay(turns []*Turn, metrics []telemetry.ClaudeMetric, loc *time.Location) []DayMinutes {
	byDay := map[string]float64{}
	cli := false
	for _, m := range metrics {
		if m.Metric == activeTimeMetric && m.Type == "cli" && !m.Time.IsZero() {
			byDay[DayIn(m.Time, loc)] += m.Value / 60
			cli = true
		}
	}
	if !cli {
		for _, t := range turns {
			for _, d := range SplitByDayIn(t.Start, t.End, loc) {
				byDay[d.Date] += d.Minutes
			}
		}
	}
	days := make([]DayMinutes, 0, len(byDay))
	for date, m := range byDay {
		days = append(days, DayMinutes{Date: date, Minutes: m})
	}
	slices.SortFunc(days, func(a, b DayMinutes) int {
		switch {
		case a.Date < b.Date:
			return -1
		case a.Date > b.Date:
			return 1
		}
		return 0
	})
	return days
}
