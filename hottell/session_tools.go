package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP-тулы анализа сессий: инвентарь, чтение, агрегаты, отгрузка истории.

func registerSessionTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "sessions_list",
		Description: "Инвентарь транскриптов Claude Code и Codex на этой машине: id, агент, проект (cwd), размер, время. Только каталоги, тела не читаются."},
		toolSessionsList)
	mcp.AddTool(s, &mcp.Tool{Name: "session_read",
		Description: "Одна сессия постранично: ходы пользователя и агента, рассуждения, вызовы инструментов и их результаты, токены. Длинные тексты обрезаются до max_text."},
		toolSessionRead)
	mcp.AddTool(s, &mcp.Tool{Name: "session_stats",
		Description: "Агрегаты по одной сессии (id) или по периоду (since/until/project/agent): события по видам, инструменты, ошибки, токены по моделям, ходы, длительность."},
		toolSessionStats)
	mcp.AddTool(s, &mcp.Tool{Name: "sessions_ship",
		Description: "Отгрузка истории выбранных сессий в otel-lab (source=backfill). Идемпотентно: уже отгруженные события не повторяются, дописанная сессия догружается хвостом. dry_run — только посчитать."},
		toolSessionsShip)
}

type sessionsListOut struct {
	Total    int           `json:"total"`
	Returned int           `json:"returned"`
	Sessions []sessionInfo `json:"sessions"`
}

func toolSessionsList(_ context.Context, _ *mcp.CallToolRequest, in sessionFilter) (*mcp.CallToolResult, sessionsListOut, error) {
	list, total, err := listSessions(in)
	if err != nil {
		return nil, sessionsListOut{}, err
	}
	if list == nil {
		list = []sessionInfo{}
	}
	return nil, sessionsListOut{Total: total, Returned: len(list), Sessions: list}, nil
}

type sessionReadIn struct {
	Agent   string   `json:"agent,omitempty" jsonschema:"claude или codex; помогает, если id неоднозначен"`
	ID      string   `json:"id" jsonschema:"id сессии (или уникальный префикс), либо путь к .jsonl"`
	Offset  int      `json:"offset,omitempty" jsonschema:"с какого события (seq), по умолчанию 0"`
	Limit   int      `json:"limit,omitempty" jsonschema:"сколько событий, по умолчанию 100"`
	Kinds   []string `json:"kinds,omitempty" jsonschema:"только эти виды: user_message, assistant_message, reasoning, tool_call, tool_result, system, attachment, token_usage, turn_start, turn_end, compact"`
	MaxText int      `json:"max_text,omitempty" jsonschema:"обрезка текстов, байт; по умолчанию 2000, 0 — по умолчанию, -1 — без обрезки"`
}

type sessionReadOut struct {
	Session sessionInfo `json:"session"`
	Meta    *transcript `json:"meta"`
	Total   int         `json:"total_events"`
	Matched int         `json:"matched_events"`
	Next    int         `json:"next_offset,omitempty"` // 0 — дальше ничего
	Events  []sessEvent `json:"events"`
}

func toolSessionRead(_ context.Context, _ *mcp.CallToolRequest, in sessionReadIn) (*mcp.CallToolResult, sessionReadOut, error) {
	si, err := findSession(in.Agent, in.ID)
	if err != nil {
		return nil, sessionReadOut{}, err
	}
	tr, err := parseTranscriptFile(si)
	if err != nil {
		return nil, sessionReadOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	maxText := in.MaxText
	if maxText == 0 {
		maxText = 2000
	}
	want := map[string]bool{}
	for _, k := range in.Kinds {
		want[k] = true
	}
	out := sessionReadOut{Session: si, Meta: tr, Total: len(tr.Events), Events: []sessEvent{}}
	for _, ev := range tr.Events {
		if len(want) > 0 && !want[ev.Kind] {
			continue
		}
		out.Matched++
		if ev.Seq < in.Offset {
			continue
		}
		if len(out.Events) == limit {
			out.Next = ev.Seq
			break
		}
		if maxText > 0 {
			ev.Text = truncate(ev.Text, maxText)
		}
		out.Events = append(out.Events, ev)
	}
	return nil, out, nil
}

type sessionStatsIn struct {
	sessionFilter
	ID string `json:"id,omitempty" jsonschema:"одна сессия; без id — агрегат по фильтру периода"`
}

type toolStat struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

type sessionStats struct {
	Sessions      int                    `json:"sessions"`
	Events        int                    `json:"events"`
	ByKind        map[string]int         `json:"by_kind"`
	Turns         int                    `json:"turns"`
	UserMessages  int                    `json:"user_messages"`
	ToolCalls     int                    `json:"tool_calls"`
	ToolErrors    int                    `json:"tool_errors"`
	Tools         []toolStat             `json:"tools"`
	Tokens        tokenUsage             `json:"tokens"`
	TokensByModel map[string]*tokenUsage `json:"tokens_by_model"`
	First         time.Time              `json:"first,omitzero"`
	Last          time.Time              `json:"last,omitzero"`
	DurationSec   float64                `json:"duration_sec"` // одна сессия — от первого события до последнего; период — сумма по сессиям
	PerSession    []sessionBrief         `json:"per_session,omitempty"`
}

type sessionBrief struct {
	Agent       string     `json:"agent"`
	ID          string     `json:"id"`
	Cwd         string     `json:"cwd,omitempty"`
	Title       string     `json:"title,omitempty"`
	Events      int        `json:"events"`
	ToolCalls   int        `json:"tool_calls"`
	Tokens      tokenUsage `json:"tokens"`
	DurationSec float64    `json:"duration_sec"`
}

func newStats() *sessionStats {
	return &sessionStats{ByKind: map[string]int{}, TokensByModel: map[string]*tokenUsage{}}
}

// addTranscript — вклад одной сессии в агрегат; возвращает её краткую сводку.
func (st *sessionStats) addTranscript(tr *transcript, tools map[string]*toolStat) sessionBrief {
	st.Sessions++
	b := sessionBrief{Agent: tr.Agent, ID: tr.ID, Cwd: tr.Cwd, Title: tr.Title, Events: len(tr.Events)}
	callTool := map[string]string{}
	var first, last time.Time
	for _, ev := range tr.Events {
		st.Events++
		st.ByKind[ev.Kind]++
		if !ev.TS.IsZero() {
			if first.IsZero() || ev.TS.Before(first) {
				first = ev.TS
			}
			if ev.TS.After(last) {
				last = ev.TS
			}
		}
		switch ev.Kind {
		case "user_message":
			st.UserMessages++
		case "turn_start":
			st.Turns++
		case "tool_call":
			st.ToolCalls++
			b.ToolCalls++
			callTool[ev.CallID] = ev.Tool
			t := tools[ev.Tool]
			if t == nil {
				t = &toolStat{Name: ev.Tool}
				tools[ev.Tool] = t
			}
			t.Calls++
		case "tool_result":
			if ev.IsError {
				st.ToolErrors++
				if t := tools[callTool[ev.CallID]]; t != nil {
					t.Errors++
				}
			}
		case "token_usage":
			st.Tokens.add(*ev.Tokens)
			b.Tokens.add(*ev.Tokens)
			m := firstNonEmpty(ev.Model, "unknown")
			if st.TokensByModel[m] == nil {
				st.TokensByModel[m] = &tokenUsage{}
			}
			st.TokensByModel[m].add(*ev.Tokens)
		}
	}
	// у Claude нет явных границ хода — ход начинается с реплики человека
	if tr.Agent == "claude" {
		for _, ev := range tr.Events {
			if ev.Kind == "user_message" {
				st.Turns++
			}
		}
	}
	if !first.IsZero() {
		b.DurationSec = last.Sub(first).Seconds()
		st.DurationSec += b.DurationSec
		if st.First.IsZero() || first.Before(st.First) {
			st.First = first
		}
		if last.After(st.Last) {
			st.Last = last
		}
	}
	return b
}

func finishTools(st *sessionStats, tools map[string]*toolStat) {
	st.Tools = []toolStat{}
	for _, t := range tools {
		st.Tools = append(st.Tools, *t)
	}
	sort.Slice(st.Tools, func(i, j int) bool {
		if st.Tools[i].Calls != st.Tools[j].Calls {
			return st.Tools[i].Calls > st.Tools[j].Calls
		}
		return st.Tools[i].Name < st.Tools[j].Name
	})
}

func toolSessionStats(_ context.Context, _ *mcp.CallToolRequest, in sessionStatsIn) (*mcp.CallToolResult, *sessionStats, error) {
	st := newStats()
	tools := map[string]*toolStat{}
	if in.ID != "" {
		si, err := findSession(in.Agent, in.ID)
		if err != nil {
			return nil, nil, err
		}
		tr, err := parseTranscriptFile(si)
		if err != nil {
			return nil, nil, err
		}
		st.addTranscript(tr, tools)
		finishTools(st, tools)
		return nil, st, nil
	}
	f := in.sessionFilter
	if f.Limit <= 0 {
		f.Limit = 20
	}
	list, total, err := listSessions(f)
	if err != nil {
		return nil, nil, err
	}
	for _, si := range list {
		tr, err := parseTranscriptFile(si)
		if err != nil {
			continue
		}
		st.PerSession = append(st.PerSession, st.addTranscript(tr, tools))
	}
	finishTools(st, tools)
	var res *mcp.CallToolResult
	if total > len(list) {
		res = &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("учтено %d сессий из %d подходящих (limit); увеличь limit для полного агрегата", len(list), total)}}}
	}
	return res, st, nil
}
