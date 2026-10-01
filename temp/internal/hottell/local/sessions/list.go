package sessions

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The inventory of both agents' transcripts reads only directories and file names; of a
// Codex rollout it reads the first line (session_meta with the cwd), and only when the
// rollout is returned or the project filter needs it. It never reads bodies.
//   Claude Code: <projects>/<cwd with / turned into ->/<session>.jsonl,
//                subagents in <session>/subagents/agent-*.jsonl.
//   Codex:       <home>/sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl and the same under
//                archived_sessions/, either compressed to .jsonl.zst.

// Roots are the directories the transcripts are under.
type Roots struct {
	// ClaudeProjects is <claude config dir>/projects.
	ClaudeProjects string
	// CodexHome is ~/.codex or CODEX_HOME: rollouts are under sessions/ and archived_sessions/.
	CodexHome string
}

// Info describes one transcript file.
type Info struct {
	Agent      string    `json:"agent"`
	ID         string    `json:"id"`
	Parent     string    `json:"parent,omitempty"` // a Claude subagent's parent session id
	Project    string    `json:"project"`          // the cwd; for Claude, the project directory name (the encoded cwd)
	Path       string    `json:"path"`
	Started    time.Time `json:"started,omitzero"` // Codex: from the file name
	Modified   time.Time `json:"modified"`
	Size       int64     `json:"size_bytes"`
	Compressed bool      `json:"compressed,omitempty"`
}

// Filter selects transcripts by agent, file time, project and size.
type Filter struct {
	Agent            string `json:"agent,omitempty" jsonschema:"claude or codex; empty means both"`
	Since            string `json:"since,omitempty" jsonschema:"not earlier (by the file's change time): a date YYYY-MM-DD, RFC3339 or a duration back like 72h"`
	Until            string `json:"until,omitempty" jsonschema:"not later (by the file's change time): a date YYYY-MM-DD (inclusive) or RFC3339"`
	Project          string `json:"project,omitempty" jsonschema:"a substring of the project path (cwd)"`
	MinSize          int64  `json:"min_size,omitempty" jsonschema:"the least file size, bytes"`
	IncludeSubagents bool   `json:"include_subagents,omitempty" jsonschema:"include the transcripts of Claude subagents"`
	Limit            int    `json:"limit,omitempty" jsonschema:"how many to return; sessions_list 50 by default, session_stats 20"`
	Offset           int    `json:"offset,omitempty" jsonschema:"how many sessions of the list to skip (pages); 0 by default"`
}

// ParseWhen reads a date YYYY-MM-DD (local), RFC3339 or a duration back from now; an
// empty string is the zero time.
func ParseWhen(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("time %q: want a date YYYY-MM-DD, RFC3339 or a duration (72h)", s)
}

// encodeClaudeProject is how Claude Code names the project directory of a cwd.
func encodeClaudeProject(p string) string {
	return strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(p)
}

func listClaude(root string, subagents bool) []Info {
	projects, _ := os.ReadDir(root)
	var out []Info
	for _, pd := range projects {
		if !pd.IsDir() {
			continue
		}
		dir := filepath.Join(root, pd.Name())
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if f.IsDir() {
				if !subagents {
					continue
				}
				subs, _ := filepath.Glob(filepath.Join(dir, f.Name(), "subagents", "*.jsonl"))
				for _, s := range subs {
					if si, ok := statSession("claude", s, pd.Name()); ok {
						si.Parent = f.Name()
						out = append(out, si)
					}
				}
				continue
			}
			if filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			if si, ok := statSession("claude", filepath.Join(dir, f.Name()), pd.Name()); ok {
				out = append(out, si)
			}
		}
	}
	return out
}

// codexRollout reports whether name is a Codex rollout, rollout-<time>-<thread>.jsonl or
// the same compressed to .jsonl.zst, with its thread id and start.
func codexRollout(name string) (id string, started time.Time, zst, ok bool) {
	base, zst := strings.CutSuffix(name, ".zst")
	base, ok = strings.CutSuffix(base, ".jsonl")
	if !ok {
		return "", time.Time{}, false, false
	}
	rest, ok := strings.CutPrefix(base, "rollout-")
	if !ok || len(rest) <= 20 {
		return "", time.Time{}, false, false
	}
	if t, err := time.ParseInLocation("2006-01-02T15-04-05", rest[:19], time.Local); err == nil {
		started = t
	}
	return strings.TrimPrefix(rest[19:], "-"), started, zst, true
}

// listCodex lists the rollouts under sessions/ and archived_sessions/. Codex moves a rollout
// to the archive and compresses it; a thread found twice is the copy changed last. The
// project (cwd) stays empty: withCodexCwd reads it for the rollouts that need it.
func listCodex(home string) []Info {
	if home == "" {
		return nil
	}
	byID := map[string]Info{}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(home, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a missing or unreadable directory has no rollouts
			}
			id, started, zst, ok := codexRollout(d.Name())
			if !ok {
				return nil
			}
			si, ok := statSession("codex", path, "")
			if !ok {
				return nil
			}
			si.ID, si.Started, si.Compressed = id, started, zst
			if prev, dup := byID[id]; !dup || si.Modified.After(prev.Modified) {
				byID[id] = si
			}
			return nil
		})
	}
	out := make([]Info, 0, len(byID))
	for _, si := range byID {
		out = append(out, si)
	}
	return out
}

func statSession(agent, path, project string) (Info, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return Info{}, false
	}
	return Info{
		Agent: agent, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: project,
		Path: path, Modified: st.ModTime(), Size: st.Size(),
	}, true
}

// withCodexCwd fills the project of each Codex rollout from its first line.
func withCodexCwd(list []Info) {
	for i := range list {
		if list[i].Agent == "codex" {
			list[i].Project = codexFirstCwd(list[i])
		}
	}
}

// codexFirstCwd is the cwd of the first line (session_meta) and no further.
func codexFirstCwd(si Info) string {
	rc, err := open(si)
	if err != nil {
		return ""
	}
	defer rc.Close()
	line, err := bufio.NewReaderSize(io.LimitReader(rc, 4<<20), 64<<10).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "session_meta" {
		return ""
	}
	return rec.Payload.Cwd
}

// List returns the inventory under the filter, the most recently changed first (equal
// times by path, so pages keep one order), from f.Offset on and at most f.Limit long;
// total counts every match, before the offset.
func List(r Roots, f Filter, now time.Time) ([]Info, int, error) {
	all, err := matching(r, f, now)
	if err != nil {
		return nil, 0, err
	}
	total := len(all)
	out := all[min(max(f.Offset, 0), len(all)):]
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if f.Project == "" { // with a project filter matching has read them already
		withCodexCwd(out)
	}
	return out, total, nil
}

// matching returns every transcript under the filter, sorted as List returns them. since,
// until and min_size apply before anything is read from a file; the cwd of a Codex rollout
// is read only for the project filter, so without it the Codex projects stay empty.
func matching(r Roots, f Filter, now time.Time) ([]Info, error) {
	if f.Agent != "" && f.Agent != "claude" && f.Agent != "codex" {
		return nil, fmt.Errorf("agent: claude or codex, not %q", f.Agent)
	}
	since, err := ParseWhen(f.Since, now)
	if err != nil {
		return nil, fmt.Errorf("since: %w", err)
	}
	until, err := ParseWhen(f.Until, now)
	if err != nil {
		return nil, fmt.Errorf("until: %w", err)
	}
	if f.Until != "" && len(f.Until) == len("2006-01-02") {
		until = until.Add(24 * time.Hour) // a date is inclusive
	}
	var all []Info
	if f.Agent == "" || f.Agent == "claude" {
		all = append(all, listClaude(r.ClaudeProjects, f.IncludeSubagents)...)
	}
	if f.Agent == "" || f.Agent == "codex" {
		all = append(all, listCodex(r.CodexHome)...)
	}
	var out []Info
	for _, s := range all {
		if !since.IsZero() && s.Modified.Before(since) {
			continue
		}
		if !until.IsZero() && !s.Modified.Before(until) {
			continue
		}
		if f.MinSize > 0 && s.Size < f.MinSize {
			continue
		}
		if f.Project != "" {
			if s.Agent == "codex" {
				s.Project = codexFirstCwd(s)
			}
			if !projectMatches(s, f.Project) {
				continue
			}
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Info) int {
		if c := b.Modified.Compare(a.Modified); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

func projectMatches(s Info, q string) bool {
	if s.Agent == "claude" {
		return strings.Contains(strings.ToLower(s.Project), strings.ToLower(encodeClaudeProject(q)))
	}
	return strings.Contains(strings.ToLower(s.Project), strings.ToLower(q))
}

// Find returns a session by its id, a unique prefix of it, or the path of its transcript
// (.jsonl, or .jsonl.zst for a compressed Codex rollout).
func Find(r Roots, agent, id string, now time.Time) (Info, error) {
	if id == "" {
		return Info{}, errors.New("a session id is required")
	}
	if strings.HasSuffix(id, ".jsonl") || strings.HasSuffix(id, ".jsonl.zst") {
		if si, ok := byPath(agent, id); ok {
			return si, nil
		}
	}
	all, err := matching(r, Filter{Agent: agent, IncludeSubagents: true}, now)
	if err != nil {
		return Info{}, err
	}
	var hits []Info
	for _, s := range all {
		if s.ID == id {
			hits = []Info{s}
			break
		}
		if strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		withCodexCwd(hits)
		return hits[0], nil
	case 0:
		return Info{}, fmt.Errorf("session %q not found", id)
	default:
		return Info{}, fmt.Errorf("prefix %q is ambiguous: %d sessions", id, len(hits))
	}
}

// byPath describes the transcript at path; a rollout- name means Codex unless agent says
// otherwise.
func byPath(agent, path string) (Info, bool) {
	name := filepath.Base(path)
	if agent == "" {
		agent = "claude"
		if strings.HasPrefix(name, "rollout-") {
			agent = "codex"
		}
	}
	si, ok := statSession(agent, path, "")
	if !ok {
		return Info{}, false
	}
	si.Compressed = strings.HasSuffix(name, ".zst")
	if id, started, _, rollout := codexRollout(name); agent == "codex" && rollout {
		si.ID, si.Started = id, started
		si.Project = codexFirstCwd(si)
	}
	return si, true
}
