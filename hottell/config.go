package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config — весь тюнинг шиппера. Файл: ~/.config/hottell/config.json,
// переопределяется флагом -config. Отсутствие файла — работа на умолчаниях.
type Config struct {
	Endpoint        string   `json:"endpoint"`          // OTLP/HTTP logs
	TokenFile       string   `json:"token_file"`        // файл с bearer-токеном, 0600
	SpoolDir        string   `json:"spool_dir"`         // буфер: каталог, файл = событие
	Environment     string   `json:"environment"`       // deployment.environment
	HostName        string   `json:"host_name"`         // пусто — короткое имя хоста
	ServiceName     string   `json:"service_name"`      // service.name в ресурсе
	BufferMaxEvents int      `json:"buffer_max_events"` // переполнение — дроп старых
	BufferMaxMB     int      `json:"buffer_max_mb"`
	BatchSize       int      `json:"batch_size"`       // событий в одном POST
	SendTimeoutSec  int      `json:"send_timeout_sec"` // весь HTTP-запрос целиком
	LingerSec       int      `json:"linger_sec"`       // сколько дрейнер ждёт на пустом спуле
	BackoffMaxSec   int      `json:"backoff_max_sec"`
	MaxFieldBytes   int      `json:"max_field_bytes"` // обрезка tool_input/tool_response и пр.
	SendPrompts     bool     `json:"send_prompts"`    // тексты промптов из UserPromptSubmit
	DebugLog        string   `json:"debug_log"`       // пусто — молчим
	Scope           string   `json:"scope"`           // marked (по умолчанию) — только проекты с маркером .hottell; all — отовсюду
	SkipEvents      []string `json:"skip_events"`     // имена событий хуков, которые не шлются
	// EnrichTranscript — дрейнер дополняет Codex PostToolUse/Stop фактами из
	// rollout сессии (код выхода, статус, токены хода): только числа и статусы.
	EnrichTranscript bool `json:"enrich_transcript"`

	sourcePath string // откуда конфиг прочитан: передаётся дрейнеру при spawn
}

func defaultConfig() Config {
	return Config{
		Endpoint:        "https://otel.alva.dev/v1/logs",
		TokenFile:       "~/.config/hottell/token",
		SpoolDir:        "~/.local/state/hottell/spool",
		Environment:     "lab",
		ServiceName:     "agent-hooks",
		BufferMaxEvents: 10000,
		BufferMaxMB:     64,
		BatchSize:       100,
		SendTimeoutSec:  5,
		LingerSec:       5,
		BackoffMaxSec:   60,
		MaxFieldBytes:   64 * 1024,
		SendPrompts:     true,
		Scope:           "marked",
		SkipEvents:      []string{},
		// Codex PostToolUse/Stop: код выхода, статус, токены хода из rollout (enrich.go)
		EnrichTranscript: true,
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func loadConfig(path string) Config {
	cfg := defaultConfig()
	data, err := os.ReadFile(expandHome(path))
	if err == nil {
		_ = json.Unmarshal(data, &cfg) // битый конфиг — работаем на умолчаниях, агента не ломаем
	}
	cfg.TokenFile = expandHome(cfg.TokenFile)
	cfg.SpoolDir = expandHome(cfg.SpoolDir)
	cfg.DebugLog = expandHome(cfg.DebugLog)
	if cfg.HostName == "" {
		h, _ := os.Hostname()
		cfg.HostName, _, _ = strings.Cut(h, ".") // alva-mac.local -> alva-mac
	}
	return cfg
}

func (c Config) token() string {
	data, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (c Config) debugf(format string, args ...any) {
	if c.DebugLog == "" {
		return
	}
	f, err := os.OpenFile(c.DebugLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(strings.TrimRight(fmt.Sprintf(format, args...), "\n") + "\n")
}
