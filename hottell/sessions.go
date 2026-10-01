package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Инвентарь транскриптов обоих агентов — только каталоги и имена файлов
// (у Codex ещё первая строка — session_meta с cwd), тела не читаются.
//   Claude Code: ~/.claude/projects/<cwd с / → ->/<session>.jsonl,
//                субагенты — <session>/subagents/agent-*.jsonl.
//   Codex:       ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<id>.jsonl.

func claudeProjectsDir() string { return expandHome("~/.claude/projects") }
func codexSessionsDir() string  { return expandHome("~/.codex/sessions") }

type sessionInfo struct {
	Agent    string    `json:"agent"`
	ID       string    `json:"id"`
	Parent   string    `json:"parent,omitempty"` // у субагента Claude — id родительской сессии
	Project  string    `json:"project"`          // cwd; у Claude — имя каталога проекта (закодированный cwd)
	Path     string    `json:"path"`
	Started  time.Time `json:"started,omitzero"` // у Codex — из имени файла
	Modified time.Time `json:"modified"`
	Size     int64     `json:"size_bytes"`
}

type sessionFilter struct {
	Agent            string `json:"agent,omitempty" jsonschema:"claude или codex; пусто — оба"`
	Since            string `json:"since,omitempty" jsonschema:"не раньше (по изменению файла): дата YYYY-MM-DD, RFC3339 или длительность вроде 72h"`
	Until            string `json:"until,omitempty" jsonschema:"не позже (по изменению файла): дата YYYY-MM-DD или RFC3339"`
	Project          string `json:"project,omitempty" jsonschema:"подстрока пути проекта (cwd)"`
	MinSize          int64  `json:"min_size,omitempty" jsonschema:"минимальный размер файла, байт"`
	IncludeSubagents bool   `json:"include_subagents,omitempty" jsonschema:"включить транскрипты субагентов Claude"`
	Limit            int    `json:"limit,omitempty" jsonschema:"сколько вернуть, по умолчанию 50"`
}

// parseWhen — дата, RFC3339 или длительность назад от now.
func parseWhen(s string, now time.Time) (time.Time, error) {
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
	return time.Time{}, fmt.Errorf("не понял время %q: нужна дата YYYY-MM-DD, RFC3339 или длительность (72h)", s)
}

// encodeClaudeProject — так Claude Code называет каталог проекта по cwd.
func encodeClaudeProject(p string) string {
	return strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(p)
}

func listClaudeSessions(subagents bool) []sessionInfo {
	root := claudeProjectsDir()
	projects, _ := os.ReadDir(root)
	var out []sessionInfo
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

func listCodexSessions() []sessionInfo {
	var out []sessionInfo
	_ = filepath.WalkDir(codexSessionsDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		si, ok := statSession("codex", path, "")
		if !ok {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		// rollout-2026-09-23T23-47-35-<uuid>
		if rest, ok := strings.CutPrefix(name, "rollout-"); ok && len(rest) > 20 {
			if t, err := time.ParseInLocation("2006-01-02T15-04-05", rest[:19], time.Local); err == nil {
				si.Started = t
			}
			si.ID = strings.TrimPrefix(rest[19:], "-")
		}
		si.Project = codexFirstCwd(path)
		out = append(out, si)
		return nil
	})
	return out
}

func statSession(agent, path, project string) (sessionInfo, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return sessionInfo{}, false
	}
	return sessionInfo{
		Agent: agent, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: project,
		Path: path, Modified: st.ModTime(), Size: st.Size(),
	}, true
}

// codexFirstCwd — cwd из первой строки (session_meta), не дальше.
func codexFirstCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(io.LimitReader(f, 4<<20), 64<<10).ReadBytes('\n')
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

// listSessions — инвентарь с фильтрами, свежие сверху; total — до limit.
func listSessions(f sessionFilter) ([]sessionInfo, int, error) {
	now := time.Now()
	since, err := parseWhen(f.Since, now)
	if err != nil {
		return nil, 0, err
	}
	until, err := parseWhen(f.Until, now)
	if err != nil {
		return nil, 0, err
	}
	if f.Until != "" && len(f.Until) == len("2006-01-02") {
		until = until.Add(24 * time.Hour) // дата включительно
	}
	var all []sessionInfo
	if f.Agent == "" || f.Agent == "claude" {
		all = append(all, listClaudeSessions(f.IncludeSubagents)...)
	}
	if f.Agent == "" || f.Agent == "codex" {
		all = append(all, listCodexSessions()...)
	}
	if f.Agent != "" && f.Agent != "claude" && f.Agent != "codex" {
		return nil, 0, fmt.Errorf("agent: claude или codex, не %q", f.Agent)
	}
	var out []sessionInfo
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
		if f.Project != "" && !projectMatches(s, f.Project) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	total := len(out)
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, total, nil
}

func projectMatches(s sessionInfo, q string) bool {
	if s.Agent == "claude" {
		return strings.Contains(strings.ToLower(s.Project), strings.ToLower(encodeClaudeProject(q)))
	}
	return strings.Contains(strings.ToLower(s.Project), strings.ToLower(q))
}

// findSession — сессия по id (или его уникальному префиксу), либо по пути к файлу.
func findSession(agent, id string) (sessionInfo, error) {
	if id == "" {
		return sessionInfo{}, fmt.Errorf("нужен id сессии")
	}
	if strings.HasSuffix(id, ".jsonl") {
		if _, err := os.Stat(id); err == nil {
			a := agent
			if a == "" {
				a = "claude"
				if strings.HasPrefix(filepath.Base(id), "rollout-") {
					a = "codex"
				}
			}
			si, _ := statSession(a, id, "")
			return si, nil
		}
	}
	all, _, err := listSessions(sessionFilter{Agent: agent, IncludeSubagents: true, Limit: 1 << 30})
	if err != nil {
		return sessionInfo{}, err
	}
	var hits []sessionInfo
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
		if strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return sessionInfo{}, fmt.Errorf("сессия %q не найдена", id)
	default:
		return sessionInfo{}, fmt.Errorf("префикс %q неоднозначен: %d сессий", id, len(hits))
	}
}
