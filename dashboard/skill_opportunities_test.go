package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillInventoryFreshnessChecksInstalledFiles(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "skills", "playwright", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: playwright\ndescription: browser\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Second)
	items := []SkillInventoryItem{{PathClass: "~/.codex/skills/playwright"}}
	if !skillInventoryCurrent(at.Format(time.RFC3339Nano), items, home) {
		t.Fatal("unchanged installed skill was marked stale")
	}
	plain := filepath.Join(home, ".codex", "skills", "plain-not-in-inventory", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(plain), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("# No frontmatter\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !skillInventoryCurrent(at.Format(time.RFC3339Nano), items, home) {
		t.Fatal("plain Markdown changed the frontmatter inventory")
	}
	added := filepath.Join(home, ".codex", "skills", "added", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(added), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(added, []byte("---\nname: added\ndescription: new skill\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if skillInventoryCurrent(at.Format(time.RFC3339Nano), items, home) {
		t.Fatal("new installed skill was marked current")
	}
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	changed := at.Add(time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	if skillInventoryCurrent(at.Format(time.RFC3339Nano), items, home) {
		t.Fatal("modified installed skill was marked current")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if skillInventoryCurrent(at.Format(time.RFC3339Nano), items, home) {
		t.Fatal("removed installed skill was marked current")
	}
}
