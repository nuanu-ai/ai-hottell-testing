package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Хук-режим: агент (Claude Code или Codex) запускает бинарь и подаёт JSON
// события на stdin. Путь горячий и обязан быть коротким и безотказным:
// локальная запись в спул, запуск дрейнера, выход 0. Сети здесь нет.
// Выход всегда 0: код 2 у хуков блокирует действие агента, любой другой
// ненулевой — шумит ошибкой; телеметрия не имеет права мешать работе.

const stdinLimit = 20 * 1024 * 1024

func runHook(cfg Config, agent string) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, stdinLimit))
	if err != nil || len(raw) == 0 {
		cfg.debugf("hook(%s): пустой или нечитаемый stdin: %v", agent, err)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		cfg.debugf("hook(%s): stdin не JSON: %v", agent, err)
		return
	}
	name, _ := payload["hook_event_name"].(string)
	if name == "" {
		name = "Unknown"
	}
	cwd, _ := payload["cwd"].(string)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	d := decideScope(cfg, name, cwd)
	if !d.Send {
		cfg.debugf("hook(%s): %s не шлётся: %s", agent, name, d.Reason)
		return
	}
	if !d.SendPrompts {
		if _, ok := payload["prompt"]; ok {
			payload["prompt"] = "[выключено send_prompts]"
		}
	}
	if name == "SessionStart" {
		addSkillSnapshot(payload, cwd, cfg.MaxFieldBytes)
	}
	ev := event{TS: time.Now().UnixNano(), Agent: agent, Event: name, Payload: payload}
	data, err := json.Marshal(ev)
	if err != nil {
		cfg.debugf("hook(%s): сериализация: %v", agent, err)
		return
	}
	if err := spoolWrite(cfg, data); err != nil {
		cfg.debugf("hook(%s): спул: %v", agent, err)
		return
	}
	// Production entry points set sourcePath. A direct library/test invocation
	// without a config path must not re-exec the test binary as a drainer.
	if cfg.sourcePath != "" {
		spawnDrainer(cfg)
	}
}

// spawnDrainer запускает дрейнер отвязанным (свой session id): хук Клода
// синхронный, и когда агент добьёт процесс-группу хука по таймауту, дрейнер
// должен выжить. Если дрейнер уже работает — новый увидит занятый flock
// и сразу выйдет; лишний spawn дешевле проверки.
func spawnDrainer(cfg Config) {
	self, err := os.Executable()
	if err != nil {
		cfg.debugf("spawn: свой путь: %v", err)
		return
	}
	cmd := exec.Command(self, "drain", "-config", cfg.sourcePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		cfg.debugf("spawn: %v", err)
		return
	}
	// Не ждём: родитель выходит, потомка переусыновит launchd/init.
	_ = cmd.Process.Release()
}
