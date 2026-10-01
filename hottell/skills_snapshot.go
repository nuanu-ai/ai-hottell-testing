package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// skillSnapshot records files installed at SessionStart. A file on disk does
// not prove that the agent offered or used the skill; plugin skills are not
// enumerated here. The analyzer must keep those distinctions.
type skillSnapshot struct {
	CapturedAt string      `json:"captured_at"`
	Source     string      `json:"source"`
	Complete   bool        `json:"complete"`
	LimitHit   bool        `json:"limit_hit"`
	Items      []skillFile `json:"items"`
}

type skillFile struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SHA256      string `json:"sha256"`
	ModifiedAt  string `json:"modified_at"`
	PathClass   string `json:"path_class"`
}

const maxSkillFileBytes = 128 * 1024

func addSkillSnapshot(payload map[string]any, cwd string, maxFieldBytes int) {
	snapshot := collectSkillSnapshot(cwd, maxFieldBytes)
	data, err := json.Marshal(snapshot)
	if err != nil || (maxFieldBytes > 0 && len(data) > maxFieldBytes) {
		payload["hottell.skill_snapshot_status"] = "field_limit_too_small"
		return
	}
	payload["hottell.skill_snapshot"] = snapshot
}

func collectSkillSnapshot(cwd string, maxFieldBytes int) skillSnapshot {
	snapshot := skillSnapshot{CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Source: "installed_filesystem_skills; plugins_excluded", Items: []skillFile{}}
	home, err := os.UserHomeDir()
	if err != nil {
		return snapshot
	}
	roots := []struct{ dir, class string }{
		{filepath.Join(home, ".codex", "skills"), "~/.codex/skills"},
		{filepath.Join(home, ".agents", "skills"), "~/.agents/skills"},
	}
	if cwd != "" {
		roots = append(roots,
			struct{ dir, class string }{filepath.Join(cwd, ".codex", "skills"), "<project>/.codex/skills"},
			struct{ dir, class string }{filepath.Join(cwd, ".agents", "skills"), "<project>/.agents/skills"},
		)
	}
	seen := map[string]bool{}
	for _, root := range roots {
		paths, err := filepath.Glob(filepath.Join(root.dir, "*", "SKILL.md"))
		if err != nil {
			continue
		}
		if root.class == "~/.codex/skills" {
			system, _ := filepath.Glob(filepath.Join(root.dir, ".system", "*", "SKILL.md"))
			paths = append(paths, system...)
		}
		for _, path := range paths {
			if seen[path] {
				continue
			}
			seen[path] = true
			item, ok := readSkillFile(path, root.dir, root.class)
			if !ok {
				continue
			}
			snapshot.Items = append(snapshot.Items, item)
		}
	}
	sort.Slice(snapshot.Items, func(i, j int) bool { return snapshot.Items[i].PathClass < snapshot.Items[j].PathClass })
	// OTel represents this object as one string attribute. Keep it below the
	// configured field limit so truncation never turns the JSON invalid.
	limit := maxFieldBytes
	if limit <= 0 || limit > 60*1024 {
		limit = 60 * 1024
	}
	for len(snapshot.Items) > 0 {
		data, _ := json.Marshal(snapshot)
		if len(data) <= limit {
			break
		}
		snapshot.Items = snapshot.Items[:len(snapshot.Items)-1]
		snapshot.LimitHit = true
	}
	return snapshot
}

func readSkillFile(path, root, class string) (skillFile, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSkillFileBytes {
		return skillFile{}, false
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) > maxSkillFileBytes {
		return skillFile{}, false
	}
	name, description := skillFrontmatter(content)
	if name == "" {
		return skillFile{}, false
	}
	pathClass, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || strings.HasPrefix(pathClass, "..") {
		return skillFile{}, false
	}
	hash := sha256.Sum256(content)
	return skillFile{
		Name: name, Description: description, SHA256: hex.EncodeToString(hash[:]),
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		PathClass:  class + "/" + filepath.ToSlash(pathClass),
	}, true
}

func skillFrontmatter(content []byte) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", ""
	}
	name, description := "", ""
	closed := false
	descriptionBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		if descriptionBlock {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				part := strings.TrimSpace(line)
				if part != "" {
					if description != "" {
						description += " "
					}
					description += part
				}
				continue
			}
			descriptionBlock = false
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			if value == "|" || value == ">" || value == "|-" || value == ">-" {
				descriptionBlock = true
			} else {
				description = value
			}
		}
	}
	if !closed || len(name) > 120 || len(description) > 500 {
		return "", ""
	}
	return name, description
}
