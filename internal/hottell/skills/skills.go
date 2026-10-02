// Package skills collects the snapshot of the skills installed for an agent, which goes
// as a hottell-skills record (docs/specs/hottell-contract/ingest.md, «Снимок skills»).
//
// The snapshot lists SKILL.md files on disk: it does not prove that the agent offered or
// used a skill, and skills of plugins are not enumerated. Only the frontmatter's name and
// description, the file's hash and time and a path relative to its root leave the Mac.
package skills

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
)

// Limits of the snapshot (ingest.md, «Снимок skills», «Лимиты»).
const (
	// MaxFileBytes bounds one SKILL.md; a larger file is left out.
	MaxFileBytes = 128 << 10
	// MaxSnapshotBytes bounds the encoded snapshot; the last skills are dropped to fit.
	MaxSnapshotBytes = 60 << 10
	// MaxRootEntries bounds the entries read from one root; the rest are left out.
	MaxRootEntries = 1000
	// CollectBudget bounds the time of one collection: no file is started after it, and
	// the files not read by then are left out.
	CollectBudget = 10 * time.Second
)

// Bounds of the frontmatter fields: a skill with a longer one is left out.
const (
	maxNameRunes        = 120
	maxDescriptionRunes = 500
)

// SourceInstalled is the snapshot's source: installed files, plugins excluded.
const SourceInstalled = "installed_filesystem_skills; plugins_excluded"

// timeFormat is RFC 3339 with the fraction of a second, as the snapshot writes times.
const timeFormat = time.RFC3339Nano

// Snapshot is the body of a hottell-skills record.
type Snapshot struct {
	CapturedAt string `json:"captured_at"`
	Source     string `json:"source"`
	// Complete is always false: the skills of plugins are not enumerated.
	Complete bool `json:"complete"`
	// LimitHit is set when a file or the snapshot was over its limit.
	LimitHit bool   `json:"limit_hit"`
	Items    []Item `json:"items"`
}

// Item is one installed skill.
type Item struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SHA256      string `json:"sha256"`
	ModifiedAt  string `json:"modified_at"`
	// PathClass is the skill's directory relative to its root, such as
	// ~/.claude/skills/<dir> or <project>/.agents/skills/<dir>.
	PathClass string `json:"path_class"`
}

// root is a directory whose subdirectories are skills; class is how the snapshot names
// it.
type root struct {
	dir, class string
}

// Collect lists the skills of agent installed under home and, when cwd is an absolute
// path, under the project in cwd, as captured at now. An unknown agent, a missing home or
// an unreadable root gives fewer items, never an error.
func Collect(agent policy.Agent, home, cwd string, now time.Time) Snapshot {
	return collect(agent, home, cwd, now, CollectBudget)
}

// collect is Collect within budget instead of CollectBudget.
func collect(agent policy.Agent, home, cwd string, now time.Time, budget time.Duration) Snapshot {
	s := Snapshot{CapturedAt: now.UTC().Format(timeFormat), Source: SourceInstalled, Items: []Item{}}
	deadline := time.Now().Add(budget)
	for _, r := range roots(agent, home, cwd) {
		names, more := readRoot(r.dir)
		s.LimitHit = s.LimitHit || more
		for _, name := range names {
			if time.Now().After(deadline) {
				s.LimitHit = true
				break
			}
			item, ok, tooBig := readSkill(filepath.Join(r.dir, name, "SKILL.md"), r.class+"/"+name)
			s.LimitHit = s.LimitHit || tooBig
			if ok {
				s.Items = append(s.Items, item)
			}
		}
	}
	slices.SortFunc(s.Items, func(a, b Item) int { return strings.Compare(a.PathClass, b.PathClass) })
	s.fit()
	return s
}

// readRoot returns the names of up to MaxRootEntries entries of dir, sorted, and whether
// it has more. Which entries a root over the limit gives is up to the file system. A
// missing or unreadable root has none.
func readRoot(dir string) (names []string, more bool) {
	// O_NONBLOCK: a FIFO in place of the root would block the open until a writer came.
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.IsDir() {
		return nil, false
	}
	names, err = f.Readdirnames(MaxRootEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	if len(names) > MaxRootEntries {
		names, more = names[:MaxRootEntries], true
	}
	slices.Sort(names)
	return names, more
}

// Encode returns the snapshot as the record's body.
func (s Snapshot) Encode() ([]byte, error) {
	if s.Items == nil {
		s.Items = []Item{}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode the skills snapshot: %w", err)
	}
	return data, nil
}

// fit drops the last items until the encoded snapshot is within MaxSnapshotBytes. It
// encodes each item once and adds up the sizes: the snapshot is its fields around an
// array of the items, separated by commas.
func (s *Snapshot) fit() {
	if data, err := s.Encode(); err == nil && len(data) <= MaxSnapshotBytes {
		return
	}
	items := s.Items
	s.Items, s.LimitHit = items[:0], true
	empty, err := s.Encode()
	if err != nil {
		return
	}
	size := len(empty)
	for i, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			break
		}
		if i > 0 {
			size++
		}
		size += len(data)
		if size > MaxSnapshotBytes {
			break
		}
		s.Items = items[:i+1]
	}
}

// roots are the skill roots of agent; the project ones only for an absolute cwd.
func roots(agent policy.Agent, home, cwd string) []root {
	var global, project []root
	switch agent {
	case policy.Claude:
		global = []root{{filepath.Join(".claude", "skills"), "~/.claude/skills"}}
		project = []root{{filepath.Join(".claude", "skills"), "<project>/.claude/skills"}}
	case policy.Codex:
		global = []root{
			{filepath.Join(".codex", "skills"), "~/.codex/skills"},
			{filepath.Join(".codex", "skills", ".system"), "~/.codex/skills/.system"},
			{filepath.Join(".agents", "skills"), "~/.agents/skills"},
		}
		project = []root{
			{filepath.Join(".codex", "skills"), "<project>/.codex/skills"},
			{filepath.Join(".agents", "skills"), "<project>/.agents/skills"},
		}
	default:
		return nil
	}
	var out []root
	if filepath.IsAbs(home) {
		for _, r := range global {
			out = append(out, root{filepath.Join(home, r.dir), r.class})
		}
	}
	if filepath.IsAbs(cwd) {
		for _, r := range project {
			dir := filepath.Join(cwd, r.dir)
			// A session in the home directory: the project root is a global one.
			if !slices.ContainsFunc(out, func(g root) bool { return g.dir == dir }) {
				out = append(out, root{dir, r.class})
			}
		}
	}
	return out
}

// readSkill reads the SKILL.md at path. It reports ok for a regular file within
// MaxFileBytes with a valid frontmatter, and tooBig for a regular file over the limit.
// A symbolic link is not followed.
func readSkill(path, class string) (item Item, ok, tooBig bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Item{}, false, false
	}
	if info.Size() > MaxFileBytes {
		return Item{}, false, true
	}
	// O_NOFOLLOW: the file may have been replaced by a link since Lstat; O_NONBLOCK: or
	// by a FIFO, which would block the open until a writer came.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Item{}, false, false
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Item{}, false, false
	}
	content, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Item{}, false, false
	}
	if len(content) > MaxFileBytes {
		return Item{}, false, true
	}
	name, description := frontmatter(content)
	if name == "" {
		return Item{}, false, false
	}
	hash := sha256.Sum256(content)
	return Item{
		Name:        name,
		Description: description,
		SHA256:      hex.EncodeToString(hash[:]),
		ModifiedAt:  info.ModTime().UTC().Format(timeFormat),
		PathClass:   class,
	}, true, false
}

// frontmatter returns the name and the description of the YAML frontmatter between two
// --- lines, a block description (| or >) joined with spaces. Without a closing fence,
// or with a field over its bound, the name is empty.
func frontmatter(content []byte) (name, description string) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 4096), MaxFileBytes+1)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", ""
	}
	closed, block := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		if block {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				if part := strings.TrimSpace(line); part != "" {
					if description != "" {
						description += " "
					}
					description += part
				}
				continue
			}
			block = false
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			switch value {
			case "|", ">", "|-", ">-":
				block = true
			default:
				description = value
			}
		}
	}
	if !closed || utf8.RuneCountInString(name) > maxNameRunes || utf8.RuneCountInString(description) > maxDescriptionRunes {
		return "", ""
	}
	return name, description
}
