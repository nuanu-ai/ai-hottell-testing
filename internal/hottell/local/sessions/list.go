package sessions

import (
	"bufio"
	"context"
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
// Codex rollout it reads the records up to its cwd (session_meta, the first line, or else
// turn_context), and only when the rollout is returned or the project filter or Allow needs
// it. It never reads bodies.
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
	// Allow reports whether a session may be shown, by its agent, its id and the first cwd its
	// transcript records; nil allows every session. A session it refuses, or one whose cwd
	// cannot be determined while it is set, is treated as one that does not exist.
	Allow func(agent, id, cwd string) bool
	// NewAllow, when set, gives each call — List, Find, Read, Source, StatsOf — its Allow from
	// one snapshot of the settings, so that one answer never mixes two versions of them; it
	// replaces Allow. A nil func it returns allows nothing.
	NewAllow func() func(agent, id, cwd string) bool
}

// request fixes the roots for one call: the Allow of one settings snapshot.
func (r Roots) request() Roots {
	if r.NewAllow == nil {
		return r
	}
	r.Allow, r.NewAllow = r.NewAllow(), nil
	if r.Allow == nil {
		r.Allow = func(_, _, _ string) bool { return false }
	}
	return r
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

	limits readLimits  // zero: the package's bounds; the tests narrow them
	file   os.FileInfo // the file judged when the session was found; open reads only that one
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

// listClaude lists the Claude Code transcripts. A link, and a subagent transcript (its
// subagents/ directory may be a link), counts only when it leads to a file under Claude's
// root, so the other agent's transcript is never judged as Claude's.
func listClaude(r Roots, subagents bool) []Info {
	root := r.ClaudeProjects
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
					if !r.owns("claude", s) {
						continue
					}
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
			p := filepath.Join(dir, f.Name())
			if f.Type()&fs.ModeSymlink != 0 && !r.owns("claude", p) {
				continue
			}
			if si, ok := statSession("claude", p, pd.Name()); ok {
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

// listCodex lists the rollouts under sessions/ and archived_sessions/, every copy of a
// thread (matching keeps one, see newestCopies). The project (cwd) stays empty: withCodexCwd
// reads it for the rollouts that need it. A link counts only when it leads to a file under
// Codex's roots.
func listCodex(r Roots) []Info {
	home := r.CodexHome
	if home == "" {
		return nil
	}
	var out []Info
	for _, dir := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(home, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a missing or unreadable directory has no rollouts
			}
			id, started, zst, ok := codexRollout(d.Name())
			if !ok || (d.Type()&fs.ModeSymlink != 0 && !r.owns("codex", path)) {
				return nil
			}
			si, ok := statSession("codex", path, "")
			if !ok {
				return nil
			}
			si.ID, si.Started, si.Compressed = id, started, zst
			out = append(out, si)
			return nil
		})
	}
	return out
}

// newestCopies keeps one copy of each Codex thread, the one changed last: Codex moves a
// rollout to the archive and compresses it, so a thread can be found twice. It runs after
// r.Allow, so a newer denied copy does not hide an older allowed one.
func newestCopies(list []Info) []Info {
	at := map[string]int{}
	out := list[:0]
	for _, s := range list {
		if s.Agent != "codex" {
			out = append(out, s)
			continue
		}
		if i, dup := at[s.ID]; dup {
			if s.Modified.After(out[i].Modified) {
				out[i] = s
			}
			continue
		}
		at[s.ID] = len(out)
		out = append(out, s)
	}
	return out
}

// rootAgent is the agent whose transcripts' root holds path once its links are followed:
// claude for the Claude projects directory, codex for Codex's sessions/ and archived_sessions/.
// A path under none of them belongs to no agent; a root that does not resolve holds nothing.
// Of nested roots the deepest one decides.
func rootAgent(r Roots, path string) (string, bool) {
	owner, _, ok := rootAgentTarget(r, path)
	return owner, ok
}

// rootAgentTarget is rootAgent with the file path leads to once its links are followed: the
// file the root was judged on.
func rootAgentTarget(r Roots, path string) (string, string, bool) {
	if !filepath.IsAbs(path) {
		return "", "", false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", false
	}
	roots := map[string]string{r.ClaudeProjects: "claude"}
	if r.CodexHome != "" {
		roots[filepath.Join(r.CodexHome, "sessions")] = "codex"
		roots[filepath.Join(r.CodexHome, "archived_sessions")] = "codex"
	}
	owner, depth := "", -1
	for root, agent := range roots {
		if root == "" {
			continue
		}
		dir, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(dir, target)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && len(dir) > depth {
			owner, depth = agent, len(dir)
		}
	}
	return owner, target, owner != ""
}

// owns reports whether path, its links followed, is under agent's root.
func (r Roots) owns(agent, path string) bool {
	owner, ok := rootAgent(r, path)
	return ok && owner == agent
}

// statSession describes the transcript at path, its links followed. Only a regular file is
// one: opening a named pipe or a device would block or read what is not a transcript.
func statSession(agent, path, project string) (Info, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return Info{}, false
	}
	return Info{
		Agent: agent, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: project,
		Path: path, Modified: st.ModTime(), Size: st.Size(), file: st,
	}, true
}

// withCodexCwd fills the project of each Codex rollout from its first line.
func withCodexCwd(ctx context.Context, list []Info) {
	for i := range list {
		if list[i].Agent == "codex" {
			list[i].Project = codexFirstCwd(ctx, list[i])
		}
	}
}

// firstCwdBytes is how much of a transcript the search for its first cwd reads.
const firstCwdBytes = 4 << 20

// codexFirstCwd is the cwd of the first session_meta or turn_context record that has one —
// session_meta is the first line, turn_context the cwd of a turn when it is not — reading at
// most 4 MiB; "" when none is found there.
func codexFirstCwd(ctx context.Context, si Info) string {
	rc, err := openUpTo(ctx, si, firstCwdBytes)
	if err != nil {
		return ""
	}
	defer rc.Close()
	br := bufio.NewReaderSize(io.LimitReader(rc, firstCwdBytes), 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &rec) == nil && (rec.Type == "session_meta" || rec.Type == "turn_context") && rec.Payload.Cwd != "" {
			return rec.Payload.Cwd
		}
		if err != nil {
			return ""
		}
	}
}

// claudeFirstCwd is the cwd of the first record of a Claude Code transcript that has one;
// the first records usually carry it. It reads at most 4 MiB.
func claudeFirstCwd(ctx context.Context, si Info) string {
	rc, err := openUpTo(ctx, si, firstCwdBytes)
	if err != nil {
		return ""
	}
	defer rc.Close()
	br := bufio.NewReaderSize(io.LimitReader(rc, firstCwdBytes), 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Cwd != "" {
			return rec.Cwd
		}
		if err != nil {
			return ""
		}
	}
}

// allows applies r.Allow to a session with the cwd its transcript records: for Codex the
// session_meta line (or the first turn_context), for Claude Code the first record with a cwd
// (the project directory name cannot be decoded back into a cwd). A session whose cwd cannot
// be determined that way — none recorded, past the bytes read for it, a file that does not
// read — is refused: whether its folder is denied is unknown.
func (r Roots) allows(ctx context.Context, si Info) bool {
	if r.Allow == nil {
		return true
	}
	cwd := si.Project
	switch {
	case si.Agent == "claude":
		cwd = claudeFirstCwd(ctx, si)
	case cwd == "":
		cwd = codexFirstCwd(ctx, si)
	}
	return cwd != "" && r.Allow(si.Agent, si.ID, cwd)
}

// List returns the inventory under the filter, the most recently changed first (equal
// times by path, so pages keep one order), from f.Offset on and at most f.Limit long;
// total counts every match, before the offset.
func List(ctx context.Context, r Roots, f Filter, now time.Time) ([]Info, int, error) {
	r = r.request()
	all, err := matching(ctx, r, f, nil, now)
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
	if f.Project == "" && r.Allow == nil { // otherwise matching has read them already
		withCodexCwd(ctx, out)
	}
	return out, total, nil
}

// matching returns every transcript under the filter and id (nil keeps every id), sorted as
// List returns them. since, until, min_size and id apply before anything is read from a
// file; the cwd of a Codex rollout is read only for the project filter and r.Allow, so
// without them the Codex projects stay empty. r.Allow is asked last, so every session it
// refuses is left out before the copies of a thread are reduced to one, the total and the
// order, and a file the other filters leave out is not opened for it.
func matching(ctx context.Context, r Roots, f Filter, id func(string) bool, now time.Time) ([]Info, error) {
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
		all = append(all, listClaude(r, f.IncludeSubagents)...)
	}
	if f.Agent == "" || f.Agent == "codex" {
		all = append(all, listCodex(r)...)
	}
	var out []Info
	for _, s := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !since.IsZero() && s.Modified.Before(since) {
			continue
		}
		if !until.IsZero() && !s.Modified.Before(until) {
			continue
		}
		if f.MinSize > 0 && s.Size < f.MinSize {
			continue
		}
		if id != nil && !id(s.ID) {
			continue
		}
		if (f.Project != "" || r.Allow != nil) && s.Agent == "codex" {
			s.Project = codexFirstCwd(ctx, s)
		}
		if f.Project != "" && !projectMatches(s, f.Project) {
			continue
		}
		if !r.allows(ctx, s) {
			continue
		}
		out = append(out, s)
	}
	out = newestCopies(out)
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
func Find(ctx context.Context, r Roots, agent, id string, now time.Time) (Info, error) {
	r = r.request()
	if id == "" {
		return Info{}, errors.New("a session id is required")
	}
	if strings.HasSuffix(id, ".jsonl") || strings.HasSuffix(id, ".jsonl.zst") {
		if si, ok := byPath(ctx, r, agent, id); ok {
			return si, nil
		}
	}
	all, err := matching(ctx, r, Filter{Agent: agent, IncludeSubagents: true},
		func(s string) bool { return strings.HasPrefix(s, id) }, now)
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
		withCodexCwd(ctx, hits)
		return hits[0], nil
	case 0:
		return Info{}, fmt.Errorf("session %q not found", id)
	default:
		return Info{}, fmt.Errorf("prefix %q is ambiguous: %d sessions", id, len(hits))
	}
}

// byPath describes the transcript at path as one of the agent whose root holds it once its
// links are followed. A path under no root is none: the id comes from an agent's tool call,
// and hottell-local reads transcripts, not any file. Nor is a path under one agent's root
// asked for as the other agent's, or a session r.Allow refuses.
func byPath(ctx context.Context, r Roots, agent, path string) (Info, bool) {
	owner, target, ok := rootAgentTarget(r, path)
	if !ok || (agent != "" && agent != owner) {
		return Info{}, false
	}
	name := filepath.Base(path)
	si, ok := statSession(owner, path, "")
	if !ok {
		return Info{}, false
	}
	// The root was judged on one resolution of path and the file stated on another: a link
	// turned between them makes them different files, and such a path is no session (HT-382).
	if st, err := os.Stat(target); err != nil || !os.SameFile(st, si.file) {
		return Info{}, false
	}
	si.Compressed = strings.HasSuffix(name, ".zst")
	if id, started, _, rollout := codexRollout(name); owner == "codex" && rollout {
		si.ID, si.Started = id, started
		si.Project = codexFirstCwd(ctx, si)
	}
	if !r.allows(ctx, si) {
		return Info{}, false
	}
	return si, true
}
