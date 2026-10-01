package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Merge не трогает чужое, идемпотентен, uninstall снимает только своё.
func TestMergeAndRemoveSurgical(t *testing.T) {
	// Живой settings.json: чужой хук на PreToolUse и посторонние ключи.
	doc := map[string]any{
		"model": "opus",
		"env":   map[string]any{"FOO": "bar"},
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"matcher": "Bash", "hooks": []any{
					map[string]any{"type": "command", "command": "/usr/bin/other-guard"},
				}},
			},
		},
	}

	added := mergeHooks(doc, claudeEvents, "/x/hottell -agent claude")
	if len(added) != len(claudeEvents) {
		t.Fatalf("добавлено %d, ожидалось %d", len(added), len(claudeEvents))
	}
	// повтор — ничего не добавляет
	if again := mergeHooks(doc, claudeEvents, "/x/hottell -agent claude"); len(again) != 0 {
		t.Fatalf("merge не идемпотентен: %v", again)
	}
	// чужой хук жив, наш добавлен рядом
	pre := doc["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse групп: %d", len(pre))
	}

	if n := removeOurHooks(doc); n != len(claudeEvents) {
		t.Fatalf("снято %d", n)
	}
	// чужое на месте, посторонние ключи не тронуты
	pre = doc["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 || groupIsOurs(pre[0]) {
		t.Fatal("чужой хук пострадал")
	}
	if doc["model"] != "opus" {
		t.Fatal("посторонний ключ пострадал")
	}
	// повторный uninstall — ноль
	if n := removeOurHooks(doc); n != 0 {
		t.Fatalf("повторное снятие: %d", n)
	}
}

// Файл из одних наших хуков после снятия пустеет до {}.
func TestRemoveEmptiesHooksKey(t *testing.T) {
	doc := map[string]any{}
	mergeHooks(doc, codexEvents, "/x/hottell -agent codex")
	removeOurHooks(doc)
	if len(doc) != 0 {
		t.Fatalf("остался мусор: %v", doc)
	}
}

// Codex: без async (Codex 0.159 пропускает хуки с неизвестным полем целиком), matcher у tool-событий.
func TestCodexEntryShape(t *testing.T) {
	doc := map[string]any{}
	mergeHooks(doc, codexEvents, "/x/hottell -agent codex")
	hooksMap := doc["hooks"].(map[string]any)
	for _, ev := range codexEvents {
		g := hooksMap[ev.name].([]any)[0].(map[string]any)
		if _, has := g["matcher"]; has != ev.matcher {
			t.Fatalf("%s: matcher=%v", ev.name, has)
		}
		h := g["hooks"].([]any)[0].(map[string]any)
		if _, has := h["async"]; has {
			t.Fatalf("%s: async в хуке Codex", ev.name)
		}
	}
}

// Уже установленные хуки hottell с async чинятся повторным install; чужие не трогаются.
func TestCodexAsyncRepaired(t *testing.T) {
	doc := map[string]any{"hooks": map[string]any{"Stop": []any{
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/x/hottell -agent codex", "async": true}}},
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/other/tool", "async": true}}},
	}}}
	changed := mergeHooks(doc, codexEvents, "/x/hottell -agent codex")
	groups := doc["hooks"].(map[string]any)["Stop"].([]any)
	if _, has := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["async"]; has {
		t.Fatal("async у хука hottell не снят")
	}
	if _, has := groups[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["async"]; !has {
		t.Fatal("тронут чужой хук")
	}
	if len(changed) == 0 {
		t.Fatal("починка не отражена в изменениях")
	}
}

// mergeConfigFile создаёт файл с нуля и переживает round-trip через диск.
func TestMergeConfigFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	added, err := mergeConfigFile(path, codexEvents, "/x/hottell -agent codex")
	if err != nil || len(added) != len(codexEvents) {
		t.Fatalf("added=%d err=%v", len(added), err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("файл не JSON: %v", err)
	}
	// повторно — без изменений
	added, err = mergeConfigFile(path, codexEvents, "/x/hottell -agent codex")
	if err != nil || added != nil {
		t.Fatalf("повтор: added=%v err=%v", added, err)
	}
}

// Битый файл конфига — отказ merge, а не молчаливое затирание.
func TestMergeRefusesBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeConfigFile(path, claudeEvents, "/x/hottell -agent claude"); err == nil {
		t.Fatal("битый JSON не должен молча затираться")
	}
}

// Пример в examples/ должен совпадать с тем, что пишет install (codexEvents): те же события, matcher только у
// событий инструментов, без полей, которые установщик снимает.
func TestCodexExampleMatchesInstall(t *testing.T) {
	raw, err := os.ReadFile("examples/codex-hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hooks) != len(codexEvents) {
		t.Fatalf("событий в примере %d, install пишет %d", len(doc.Hooks), len(codexEvents))
	}
	for _, ev := range codexEvents {
		groups := doc.Hooks[ev.name]
		if len(groups) != 1 {
			t.Fatalf("%s: групп %d", ev.name, len(groups))
		}
		if _, has := groups[0]["matcher"]; has != ev.matcher {
			t.Fatalf("%s: matcher есть=%v, у install %v", ev.name, has, ev.matcher)
		}
		h := groups[0]["hooks"].([]any)[0].(map[string]any)
		if _, has := h["async"]; has {
			t.Fatalf("%s: в примере async, install его снимает (dropAsync)", ev.name)
		}
		if h["command"] != "hottell -agent codex" {
			t.Fatalf("%s: command=%v", ev.name, h["command"])
		}
	}
}
