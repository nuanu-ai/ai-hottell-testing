package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Отгрузка истории (backfill) в otel-lab. Решение по TTL (ClickHouse хранит
// 10 суток по Timestamp): Timestamp записи = время загрузки, родное время
// события — атрибутом event.time. Ресурс service.name=agent-backfill, чтобы
// история не смешивалась с живым потоком agent-hooks; у каждой записи
// source=backfill. Отправка — прямо батчами, мимо спула: это разовая
// операция из MCP, а не горячий путь хука.
//
// Идемпотентность: реестр <state>/shipped.json хранит по сессии, сколько
// событий потока уже доставлено. Транскрипты только дописываются, поэтому
// повторная отгрузка шлёт хвост events[shipped:] — ноль для неизменной сессии.

const backfillService = "agent-backfill"

type shippedEntry struct {
	Events    int       `json:"events"`
	Size      int64     `json:"size_bytes"`
	ShippedAt time.Time `json:"shipped_at"`
}

func shipRegistryPath(cfg Config) string {
	return filepath.Join(filepath.Dir(cfg.SpoolDir), "shipped.json")
}

func loadShipRegistry(cfg Config) map[string]shippedEntry {
	reg := map[string]shippedEntry{}
	if data, err := os.ReadFile(shipRegistryPath(cfg)); err == nil {
		_ = json.Unmarshal(data, &reg)
	}
	return reg
}

func saveShipRegistry(cfg Config, reg map[string]shippedEntry) error {
	path := shipRegistryPath(cfg)
	data, _ := json.MarshalIndent(reg, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".hottell-tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func intAttr(n int64) map[string]string {
	return map[string]string{"intValue": strconv.FormatInt(n, 10)}
}

func backfillRecord(cfg Config, tr *transcript, ev sessEvent, now time.Time) map[string]any {
	attrs := []kv{
		{Key: "agent", Value: str(tr.Agent)},
		{Key: "source", Value: str("backfill")},
		{Key: "session.id", Value: str(tr.ID)},
		{Key: "event.seq", Value: intAttr(int64(ev.Seq))},
		{Key: "event.kind", Value: str(ev.Kind)},
		{Key: "event.time", Value: str(ev.TS.UTC().Format(time.RFC3339Nano))},
	}
	add := func(k, v string) {
		if v != "" {
			attrs = append(attrs, kv{Key: k, Value: str(v)})
		}
	}
	add("session.cwd", tr.Cwd)
	add("session.git_branch", tr.GitBranch)
	add("agent.version", tr.Version)
	add("role", ev.Role)
	add("tool_name", ev.Tool)
	add("call_id", ev.CallID)
	add("model", ev.Model)
	add("turn_id", ev.Turn)
	text := ev.Text
	if !cfg.SendPrompts && ev.Kind == "user_message" && text != "" {
		text = "[выключено send_prompts]"
	}
	add("text", truncate(text, cfg.MaxFieldBytes))
	if ev.IsError {
		attrs = append(attrs, kv{Key: "is_error", Value: map[string]bool{"boolValue": true}})
	}
	if t := ev.Tokens; t != nil {
		attrs = append(attrs,
			kv{Key: "tokens.input", Value: intAttr(t.Input)},
			kv{Key: "tokens.cache_read", Value: intAttr(t.CacheRead)},
			kv{Key: "tokens.cache_write", Value: intAttr(t.CacheWrite)},
			kv{Key: "tokens.output", Value: intAttr(t.Output)},
			kv{Key: "tokens.reasoning", Value: intAttr(t.Reasoning)})
	}
	if len(ev.Attrs) > 0 {
		raw, _ := json.Marshal(ev.Attrs)
		add("attrs", string(raw))
	}
	ns := strconv.FormatInt(now.UnixNano(), 10)
	return map[string]any{
		"timeUnixNano":         ns,
		"observedTimeUnixNano": ns,
		"severityNumber":       9,
		"severityText":         "INFO",
		"body":                 str("agent.session." + ev.Kind),
		"attributes":           attrs,
	}
}

type shipIn struct {
	sessionFilter
	IDs    []string `json:"ids,omitempty" jsonschema:"id сессий для отгрузки; без ids — все сессии по фильтру (limit по умолчанию 20)"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"только посчитать, ничего не отправлять"`
}

type shipResult struct {
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Total   int    `json:"total_events"`
	Already int    `json:"already_shipped"`
	Shipped int    `json:"shipped"`
	Error   string `json:"error,omitempty"`
}

type shipOut struct {
	DryRun   bool         `json:"dry_run"`
	Endpoint string       `json:"endpoint"`
	Shipped  int          `json:"shipped_events"`
	Sessions []shipResult `json:"sessions"`
	Registry string       `json:"registry"`
}

func toolSessionsShip(_ context.Context, _ *mcp.CallToolRequest, in shipIn) (*mcp.CallToolResult, shipOut, error) {
	cfg := loadConfig(defaultConfigPath)
	return nil, shipSessions(cfg, in), nil
}

func shipSessions(cfg Config, in shipIn) shipOut {
	out := shipOut{DryRun: in.DryRun, Endpoint: cfg.Endpoint, Registry: shipRegistryPath(cfg), Sessions: []shipResult{}}
	var targets []sessionInfo
	if len(in.IDs) > 0 {
		for _, id := range in.IDs {
			si, err := findSession(in.Agent, id)
			if err != nil {
				out.Sessions = append(out.Sessions, shipResult{ID: id, Error: err.Error()})
				continue
			}
			targets = append(targets, si)
		}
	} else {
		f := in.sessionFilter
		if f.Limit <= 0 {
			f.Limit = 20
		}
		targets, _, _ = listSessions(f)
	}
	reg := loadShipRegistry(cfg)
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 100
	}
	for _, si := range targets {
		key := si.Agent + ":" + si.ID
		res := shipResult{Agent: si.Agent, ID: si.ID}
		tr, err := parseTranscriptFile(si)
		if err != nil {
			res.Error = err.Error()
			out.Sessions = append(out.Sessions, res)
			continue
		}
		res.Total = len(tr.Events)
		done := reg[key].Events
		if done > res.Total {
			done = 0 // файл перезаписан, а не дописан — начинаем заново
		}
		res.Already = done
		if in.DryRun {
			res.Shipped = res.Total - done
			out.Shipped += res.Shipped
			out.Sessions = append(out.Sessions, res)
			continue
		}
		for done < res.Total {
			end := min(done+batch, res.Total)
			now := time.Now()
			records := make([]map[string]any, 0, end-done)
			for _, ev := range tr.Events[done:end] {
				records = append(records, backfillRecord(cfg, tr, ev, now))
			}
			if err := sendOTLP(cfg, otlpPayload(cfg, backfillService, records)); err != nil {
				res.Error = fmt.Sprintf("после %d событий: %v", done, err)
				break
			}
			res.Shipped += end - done
			done = end
		}
		out.Shipped += res.Shipped
		if res.Shipped > 0 {
			reg[key] = shippedEntry{Events: done, Size: si.Size, ShippedAt: time.Now().UTC()}
			if err := saveShipRegistry(cfg, reg); err != nil && res.Error == "" {
				res.Error = "реестр: " + err.Error()
			}
		}
		out.Sessions = append(out.Sessions, res)
	}
	return out
}
