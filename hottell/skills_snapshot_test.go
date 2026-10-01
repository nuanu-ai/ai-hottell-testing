package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillSnapshotRecordsInstalledFilesWithoutAbsolutePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	global := filepath.Join(home, ".codex", "skills", "example")
	local := filepath.Join(project, ".agents", "skills", "project-check")
	for _, dir := range []string{global, local} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(global, "SKILL.md"): "---\nname: example\ndescription: Browser verification\n---\nprivate steps\n",
		filepath.Join(local, "SKILL.md"):  "---\nname: project-check\ndescription: Check this project\n---\nprivate steps\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := collectSkillSnapshot(project, 64*1024)
	if snapshot.Complete || snapshot.LimitHit || len(snapshot.Items) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	data, _ := json.Marshal(snapshot)
	if strings.Contains(string(data), home) || strings.Contains(string(data), project) || strings.Contains(string(data), "private steps") {
		t.Fatalf("snapshot contains paths or body: %s", data)
	}
	paths := map[string]bool{}
	for _, item := range snapshot.Items {
		paths[item.PathClass] = true
	}
	if !paths["~/.codex/skills/example"] || !paths["<project>/.agents/skills/project-check"] {
		t.Fatalf("path classes: %+v", snapshot.Items)
	}
	for _, item := range snapshot.Items {
		if len(item.SHA256) != 64 || item.ModifiedAt == "" || item.Description == "" {
			t.Fatalf("incomplete skill metadata: %+v", item)
		}
	}
}

func TestSkillSnapshotKeepsValidJSONWithinFieldLimit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(home, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Description\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := collectSkillSnapshot("", 250)
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > 250 || !snapshot.LimitHit {
		t.Fatalf("invalid bounded snapshot: bytes=%d error=%v snapshot=%+v", len(data), err, snapshot)
	}
}

func TestTinyFieldLimitDoesNotSendBrokenSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	payload := map[string]any{}
	addSkillSnapshot(payload, "", 10)
	if _, sent := payload["hottell.skill_snapshot"]; sent || payload["hottell.skill_snapshot_status"] != "field_limit_too_small" {
		t.Fatalf("unexpected tiny-limit payload: %+v", payload)
	}
}

func TestSkillFrontmatterNeedsClosingFence(t *testing.T) {
	name, _ := skillFrontmatter([]byte("---\nname: false-positive\ndescription: no closing fence\n"))
	if name != "" {
		t.Fatalf("accepted incomplete frontmatter: %s", name)
	}
}

func TestSkillFrontmatterFoldedDescription(t *testing.T) {
	name, description := skillFrontmatter([]byte("---\nname: folded\ndescription: |\n  First line\n  second line\n---\nbody"))
	if name != "folded" || description != "First line second line" {
		t.Fatalf("name=%q description=%q", name, description)
	}
}
