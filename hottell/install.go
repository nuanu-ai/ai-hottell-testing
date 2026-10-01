package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// install/uninstall — хирургия по живому, без бэкапов: параллельные процессы
// могут переписывать конфиги агентов, и «восстановить как было при установке»
// значило бы затереть их работу. Поэтому install добавляет только свои записи
// (опознаются по слову hottell в команде) и идемпотентен; uninstall читает
// конфиг сейчас и выкидывает только свои записи, чужое не трогая.

type hookEvent struct {
	name    string
	matcher bool // событию нужен matcher "*"
}

var claudeEvents = []hookEvent{
	{"PreToolUse", true}, {"PostToolUse", true},
	{"UserPromptSubmit", false}, {"Notification", false},
	{"Stop", false}, {"SubagentStop", false},
	{"PreCompact", false}, {"SessionStart", false}, {"SessionEnd", false},
}

// SessionEnd и Interrupt у Codex синхронные: их таймаут — секунды, фон не успеет.
// Без async: Codex 0.159 не знает этого поля и пропускает такие хуки целиком.
// Он и не нужен — хук только кладёт событие в спул и выходит за миллисекунды.
var codexEvents = []hookEvent{
	{"PreToolUse", true}, {"PostToolUse", true}, {"PermissionRequest", true},
	{"UserPromptSubmit", false}, {"PreCompact", false}, {"PostCompact", false},
	{"Stop", false}, {"SubagentStart", false}, {"SubagentStop", false},
	{"SessionStart", false}, {"SessionEnd", false}, {"Interrupt", false},
}

// paths — все места, которые трогают install/uninstall/status; в тестах подменяются.
type paths struct {
	claudeSettings string
	claudeJSON     string // ~/.claude.json: MCP-серверы пользовательского уровня
	codexHooks     string
	codexConfig    string // ~/.codex/config.toml: [mcp_servers.*], [otel]
	cfgDir         string // token + config.json
	stateDir       string // спул
	binDir         string
}

func defaultPaths(binDir string) paths {
	return paths{
		claudeSettings: expandHome("~/.claude/settings.json"),
		claudeJSON:     expandHome("~/.claude.json"),
		codexHooks:     expandHome("~/.codex/hooks.json"),
		codexConfig:    expandHome("~/.codex/config.toml"),
		cfgDir:         expandHome("~/.config/hottell"),
		stateDir:       expandHome("~/.local/state/hottell"),
		binDir:         expandHome(binDir),
	}
}

func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // числа чужих ключей переживают round-trip без потери точности
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: не JSON: %w", path, err)
	}
	return doc, nil
}

func writeJSONFile(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".hottell-tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func groupIsOurs(group any) bool {
	g, ok := group.(map[string]any)
	if !ok {
		return false
	}
	hooks, _ := g["hooks"].([]any)
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "hottell") {
			return true
		}
	}
	return false
}

// mergeHooks добавляет недостающие записи hottell в doc["hooks"];
// возвращает, что изменилось. Чужие записи не трогает никогда.
func mergeHooks(doc map[string]any, events []hookEvent, command string) []string {
	hooksMap, ok := doc["hooks"].(map[string]any)
	if !ok {
		hooksMap = map[string]any{}
		doc["hooks"] = hooksMap
	}
	var added []string
	for _, ev := range events {
		groups, _ := hooksMap[ev.name].([]any)
		exists := false
		for _, g := range groups {
			if groupIsOurs(g) {
				exists = true
				if dropAsync(g) {
					added = append(added, ev.name+" (снят async)")
				}
			}
		}
		if exists {
			continue
		}
		hook := map[string]any{"type": "command", "command": command}
		group := map[string]any{"hooks": []any{hook}}
		if ev.matcher {
			group["matcher"] = "*"
		}
		hooksMap[ev.name] = append(groups, group)
		added = append(added, ev.name)
	}
	return added
}

// removeOurHooks выкидывает из doc["hooks"] только записи hottell.
func removeOurHooks(doc map[string]any) int {
	hooksMap, ok := doc["hooks"].(map[string]any)
	if !ok {
		return 0
	}
	removed := 0
	for name, v := range hooksMap {
		groups, _ := v.([]any)
		var kept []any
		for _, g := range groups {
			if groupIsOurs(g) {
				removed++
			} else {
				kept = append(kept, g)
			}
		}
		if len(kept) == 0 {
			delete(hooksMap, name)
		} else {
			hooksMap[name] = kept
		}
	}
	if len(hooksMap) == 0 {
		delete(doc, "hooks")
	}
	return removed
}

func mergeConfigFile(path string, events []hookEvent, command string) ([]string, error) {
	doc, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	added := mergeHooks(doc, events, command)
	if len(added) == 0 {
		return nil, nil
	}
	return added, writeJSONFile(path, doc)
}

func runInstall(p paths, tokenFile string, tokenStdin bool) error {
	// Токен — только явно, никакого выуживания из чужих конфигов.
	var token []byte
	var err error
	switch {
	case tokenFile != "":
		token, err = os.ReadFile(expandHome(tokenFile))
	case tokenStdin:
		token, err = io.ReadAll(io.LimitReader(os.Stdin, 64*1024))
	default:
		return runInstallLocal(p, localBase)
	}
	if err != nil {
		return fmt.Errorf("чтение токена: %w", err)
	}
	tok := strings.TrimSpace(string(token))
	if tok == "" {
		return fmt.Errorf("токен пуст")
	}
	if err := os.MkdirAll(p.cfgDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(p.cfgDir, "token"), []byte(tok+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println("токен: записан в", filepath.Join(p.cfgDir, "token"))

	// Конфиг: только если его ещё нет — существующий не перезаписываем.
	cfgPath := filepath.Join(p.cfgDir, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		def := defaultConfig()
		data, _ := json.MarshalIndent(def, "", "  ")
		if err := os.WriteFile(cfgPath, append(data, '\n'), 0o600); err != nil {
			return err
		}
		fmt.Println("конфиг: создан", cfgPath)
	} else {
		fmt.Println("конфиг: уже есть, не тронут")
	}

	// Бинарь: копия себя в binDir, если запущены не оттуда.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	target := filepath.Join(p.binDir, "hottell")
	if self != target {
		if err := copyFile(self, target, 0o755); err != nil {
			return fmt.Errorf("копия бинаря в %s: %w", target, err)
		}
		fmt.Println("бинарь:", target)
	} else {
		fmt.Println("бинарь: уже на месте")
	}

	// Хуки обоих агентов; команда — абсолютный путь, PATH хуков не наш.
	added, err := mergeConfigFile(p.claudeSettings, claudeEvents, target+" -agent claude")
	if err != nil {
		return err
	}
	report("Claude Code ("+p.claudeSettings+")", added)
	added, err = mergeConfigFile(p.codexHooks, codexEvents, target+" -agent codex")
	if err != nil {
		return err
	}
	report("Codex ("+p.codexHooks+")", added)

	// MCP-сервер у обоих агентов: тот же бинарь в режиме mcp.
	if err := registerMCP(p, target); err != nil {
		return err
	}

	// Смоук до коллектора — предупреждение, не отказ: события подождут в буфере.
	cfg := loadConfig(cfgPath)
	cfg.sourcePath = cfgPath
	ev := event{TS: time.Now().UnixNano(), Agent: "install", Event: "InstallCheck",
		Payload: map[string]any{"host": cfg.HostName}}
	if err := sendOTLP(cfg, buildOTLP(cfg, []event{ev})); err != nil {
		fmt.Println("смоук: коллектор недоступен:", err, "— события будут копиться в буфере")
	} else {
		fmt.Println("смоук: доставлено в", cfg.Endpoint)
	}

	fmt.Println("\nосталось руками: в Codex выполнить /hooks и доверить хуки;")
	fmt.Println("живые сессии обоих агентов — /hooks внутри сессии или рестарт с resume.")
	return nil
}

func runUninstall(p paths) error {
	for _, path := range []string{p.claudeSettings, p.codexHooks} {
		doc, err := readJSONFile(path)
		if err != nil {
			fmt.Println(path, "—", err, "(пропущен)")
			continue
		}
		n := removeOurHooks(doc)
		if n == 0 {
			fmt.Println(path, "— записей hottell нет")
			continue
		}
		// Файл из одних наших хуков после чистки пуст — убираем целиком.
		if len(doc) == 0 {
			if err := os.Remove(path); err != nil {
				return err
			}
			fmt.Println(path, "— удалён (были только хуки hottell)")
			continue
		}
		if err := writeJSONFile(path, doc); err != nil {
			return err
		}
		fmt.Printf("%s — снято записей: %d\n", path, n)
	}
	if err := unregisterMCP(p); err != nil {
		return err
	}
	for _, dir := range []string{p.cfgDir, p.stateDir} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		fmt.Println("удалён", dir)
	}
	target := filepath.Join(p.binDir, "hottell")
	if _, err := os.Stat(target); err == nil {
		if err := os.Remove(target); err != nil {
			return err
		}
		fmt.Println("удалён", target)
	}
	return nil
}

func runStatus(p paths) {
	target := filepath.Join(p.binDir, "hottell")
	stat := func(label string, path string) bool {
		_, err := os.Stat(path)
		if err == nil {
			fmt.Println(label+":", path)
			return true
		}
		fmt.Println(label + ": нет")
		return false
	}
	stat("бинарь", target)
	stat("токен", filepath.Join(p.cfgDir, "token"))
	stat("конфиг", filepath.Join(p.cfgDir, "config.json"))

	for agent, path := range map[string]string{"Claude Code": p.claudeSettings, "Codex": p.codexHooks} {
		doc, err := readJSONFile(path)
		if err != nil {
			fmt.Printf("хуки %s: %v\n", agent, err)
			continue
		}
		count := 0
		if hooksMap, ok := doc["hooks"].(map[string]any); ok {
			for _, v := range hooksMap {
				groups, _ := v.([]any)
				for _, g := range groups {
					if groupIsOurs(g) {
						count++
					}
				}
			}
		}
		fmt.Printf("хуки %s: %d событий (%s)\n", agent, count, path)
	}

	claudeMCP, codexMCP := mcpRegistered(p)
	fmt.Printf("MCP Claude Code: %s (%s)\n", yesNo(claudeMCP), p.claudeJSON)
	fmt.Printf("MCP Codex: %s (%s)\n", yesNo(codexMCP), p.codexConfig)

	cfg := loadConfig(filepath.Join(p.cfgDir, "config.json"))
	entries := spoolList(cfg)
	var size int64
	for _, e := range entries {
		size += e.size
	}
	fmt.Printf("буфер: %d событий, %d КБ (%s)\n", len(entries), size/1024, cfg.SpoolDir)
}

func report(label string, added []string) {
	if len(added) == 0 {
		fmt.Println(label + ": хуки уже стоят")
		return
	}
	fmt.Printf("%s: добавлено %d событий: %s\n", label, len(added), strings.Join(added, ", "))
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".hottell-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Локальный режим: без токена, без вопросов, всё и отовсюду на локальный стенд
// (local/docker-compose.yml). Хуки обоих агентов, нативный OTel обоих агентов
// по максимуму, MCP, тестовое событие.
const localBase = "http://localhost:4318"

// claudeLocalEnv — всё, что Claude Code умеет отдавать нативно.
var claudeLocalEnv = map[string]string{
	"CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
	"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
	"OTEL_METRICS_EXPORTER":               "otlp",
	"OTEL_LOGS_EXPORTER":                  "otlp",
	"OTEL_TRACES_EXPORTER":                "otlp",
	"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
	"OTEL_METRIC_EXPORT_INTERVAL":         "10000",
	"OTEL_LOGS_EXPORT_INTERVAL":           "2000",
	"OTEL_TRACES_EXPORT_INTERVAL":         "2000",
	"OTEL_LOG_USER_PROMPTS":               "1",
	"OTEL_LOG_TOOL_DETAILS":               "1",
	"OTEL_LOG_TOOL_CONTENT":               "1",
	"OTEL_LOG_ASSISTANT_RESPONSES":        "1",
	"OTEL_LOG_RAW_API_BODIES":             "1",
}

func runInstallLocal(p paths, base string) error {
	if err := os.MkdirAll(p.cfgDir, 0o700); err != nil {
		return err
	}
	// Конфиг hottell: локальный приём, отовсюду, с промптами.
	cfgPath := filepath.Join(p.cfgDir, "config.json")
	cfg := defaultConfig()
	cfg.Endpoint = base + "/v1/logs"
	cfg.Scope = "all"
	cfg.SendPrompts = true
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Println("конфиг:", cfgPath, "— приём", cfg.Endpoint, "— отовсюду, с промптами")

	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	target := filepath.Join(p.binDir, "hottell")
	if self != target {
		if err := copyFile(self, target, 0o755); err != nil {
			return fmt.Errorf("копия бинаря в %s: %w", target, err)
		}
	}
	fmt.Println("бинарь:", target)

	added, err := mergeConfigFile(p.claudeSettings, claudeEvents, target+" -agent claude")
	if err != nil {
		return err
	}
	report("хуки Claude Code ("+p.claudeSettings+")", added)
	added, err = mergeConfigFile(p.codexHooks, codexEvents, target+" -agent codex")
	if err != nil {
		return err
	}
	report("хуки Codex ("+p.codexHooks+")", added)

	// Нативный OTel Claude Code: наши ключи целиком, чужие env не трогаем.
	doc, err := readJSONFile(p.claudeSettings)
	if err != nil {
		return err
	}
	env := envOf(doc)
	if env == nil {
		env = map[string]any{}
	}
	delete(env, "OTEL_EXPORTER_OTLP_HEADERS") // локально без токена
	for k, v := range claudeLocalEnv {
		env[k] = v
	}
	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = base
	env["OTEL_RESOURCE_ATTRIBUTES"] = "host.name=" + loadConfig(cfgPath).HostName + ",deployment.environment=local"
	doc["env"] = env
	if err := writeJSONFile(p.claudeSettings, doc); err != nil {
		return err
	}
	fmt.Println("нативный OTel Claude Code: всё (метрики, логи, трейсы, промпты, инструменты, ответы) ->", base)

	// Нативный OTel Codex: семейство [otel] нашим блоком.
	text, err := readTextFile(p.codexConfig)
	if err != nil {
		return err
	}
	if err := writeTextFile(p.codexConfig, tomlSetFamily(text, "otel", codexOtelBlock("local", true, base, ""))); err != nil {
		return err
	}
	fmt.Println("нативный OTel Codex: логи, метрики, трейсы, промпты ->", base)

	if err := registerMCP(p, target); err != nil {
		return err
	}

	c := loadConfig(cfgPath)
	c.sourcePath = cfgPath
	ev := event{TS: time.Now().UnixNano(), Agent: "install", Event: "InstallCheck", Payload: map[string]any{"host": c.HostName}}
	if err := sendOTLP(c, buildOTLP(c, []event{ev})); err != nil {
		fmt.Println("смоук: стенд недоступен:", err, "— поднимите: docker compose -f local/docker-compose.yml up -d")
	} else {
		fmt.Println("смоук: доставлено в", c.Endpoint)
	}
	fmt.Println("\nданные: http://localhost:8123/play (default, без пароля), база otel")
	fmt.Println("осталось руками: в Codex выполнить /hooks и доверить хуки; перезапустить открытые сессии агентов.")
	return nil
}

// dropAsync — снимает async с хуков hottell, поставленных прежними версиями.
func dropAsync(group any) bool {
	g, _ := group.(map[string]any)
	hooks, _ := g["hooks"].([]any)
	changed := false
	for _, h := range hooks {
		if m, ok := h.(map[string]any); ok {
			if _, has := m["async"]; has {
				delete(m, "async")
				changed = true
			}
		}
	}
	return changed
}
