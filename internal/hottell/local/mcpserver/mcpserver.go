// Package mcpserver is hottell-local: the MCP server the agents start from the hottell
// binary over stdio. It gives the session-retro and hottell-coach skills this Mac's
// sessions and installed skills; nothing it reads leaves the machine. The findings and the
// coach's journal live on the service's server hottell (findings, coach_journal); this one
// never talks to it.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// Name is the server's name in the agents' configs.
const Name = "hottell-local"

// Config is what the server reads.
type Config struct {
	Roots   sessions.Roots
	Version string
	Now     func() time.Time
	// Skills lists the installed skills skills_inventory answers; nil lists none. The
	// hottell binary sets it, since the collector is its package.
	Skills func(now time.Time) []SkillItem
}

// SkillsScope is what skills_inventory covers (mcp.md, «skills_inventory»).
const SkillsScope = "user-installed local skills of Codex and Claude Code; system and plugin skills excluded"

// SkillsInventory is skills_inventory's answer.
type SkillsInventory struct {
	SnapshotAt string      `json:"snapshot_at"`
	Scope      string      `json:"scope"`
	Items      []SkillItem `json:"items"`
}

// SkillItem is one installed skill.
type SkillItem struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	PathClass         string `json:"path_class"`
	DescriptionSHA256 string `json:"description_sha256"` // of the description's UTF-8 bytes
}

// New returns the server with its tools.
func New(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: cfg.Version}, nil)
	h := handlers{cfg: cfg}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "sessions_list",
		Description: "Inventory of Claude Code and Codex transcripts on this Mac: id, agent, project (cwd), size, times. since/until look at the file's mtime, not at the events; offset/limit page the list.",
	}, h.list)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "session_read",
		Description: "One session page by page: user and agent turns, reasoning, tool calls and results, tokens. from/to keep only events of that time; matched_events and out_of_period count the whole session, next_offset is the next page (0 — none). Each event carries seq (its place in the stream) and line (its 1-based line in the transcript file, blank lines counted, of the decompressed content for .jsonl.zst; one line may give several events). from_line/to_line keep only the events of those lines: from_line=to_line=N opens the line an L-number link (LN) points at. A transcript over 512 MiB of content, with a line over 64 MiB or a zstd window over 64 MiB is refused as session_too_large; session_stats counts such a session as unreadable.",
	}, h.read)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "session_source",
		Description: "Freezes one session's transcript before an analysis: source_sha256 is the sha256 over the bytes of every line that ends in \\n, of the decompressed content for .jsonl.zst, and source_records is how many such lines there are, blank lines included, so an event line n is inside the frozen prefix when n <= source_records. A last line without \\n (still being written) is left out. frozen_at is the time of the freeze, UTC. Errors: source_invalid (a complete line is not JSON), source_empty (no complete record), session_too_large (over 512 MiB of content, a line over 64 MiB or a zstd window over 64 MiB).",
	}, h.source)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "deep_source_check",
		Description: "Checks a Deep v2 report against its frozen source before deep_submit. 1) The prefix is hashed again as session_source hashes it: fewer complete lines than source_records gives errors [\"source shorter than frozen prefix\"], another hash [\"source hash changed\"], and the check stops. 2) The report's form: one error \"<path>: <reason>\", and the check stops. 3) The roles: each task's start_line and at least one goal_evidence line must be user-role messages — Codex: response_item message of role user, never a compacted record; Claude Code: a user record whose content is a string or not tool_result blocks alone, never a compaction summary (isCompactSummary) — and each claims[].line an assistant message (response_item message of role assistant; Claude Code's assistant record with at least one text block — tool_use alone is a tool call): \"<task_id>: task starts outside a user-role message\", \"<task_id>: goal is not grounded in a user-role message\", \"<task_id>: completion claim is not an assistant message\". Answers {ok, errors}; a session not found or denied, source_invalid and session_too_large are errors of the call.",
	}, h.deepSourceCheck)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "session_stats",
		Description: "Aggregates of one session (id) or of a period: events by kind, tools and their errors, tokens by model, turns, duration. from/to count only events of that time; coverage says how many sessions and events were seen of those found, coverage.next_offset is the next page. A page may hold fewer sessions than limit: it stops at a byte budget of transcripts.",
	}, h.stats)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skills_inventory",
		Description: "The skills installed on this Mac for Codex (~/.codex/skills, ~/.agents/skills) and Claude Code (~/.claude/skills), for skill_opportunities_submit: {snapshot_at, scope, items[{name, description, path_class, description_sha256}]}, by name. A multi-line description is read as text; the Codex runtime's own skills (~/.codex/skills/.system), plugin skills and project skills are left out, so is a skill with no description; of one name the first root in that order counts. Take the inventory from here, never from text.",
	}, h.skillsInventory)
	return s
}

// Run serves one agent over in and out until it disconnects or ctx ends. Neither stream is
// closed: they are the process's stdin and stdout.
func Run(ctx context.Context, cfg Config, in io.Reader, out io.Writer) error {
	return New(cfg).Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
}

// nopWriteCloser keeps the transport from closing the process's stdout.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type handlers struct {
	cfg Config
}

type listOut struct {
	Total    int             `json:"total"`
	Returned int             `json:"returned"`
	Sessions []sessions.Info `json:"sessions"`
}

func (h handlers) list(ctx context.Context, _ *mcp.CallToolRequest, in sessions.Filter) (*mcp.CallToolResult, listOut, error) {
	list, total, err := sessions.List(ctx, h.cfg.Roots, in, h.cfg.Now())
	if err != nil {
		return nil, listOut{}, err
	}
	if list == nil {
		list = []sessions.Info{}
	}
	return nil, listOut{Total: total, Returned: len(list), Sessions: list}, nil
}

func (h handlers) read(ctx context.Context, _ *mcp.CallToolRequest, in sessions.ReadIn) (*mcp.CallToolResult, sessions.ReadOut, error) {
	out, err := sessions.Read(ctx, h.cfg.Roots, in, h.cfg.Now())
	return nil, out, err
}

func (h handlers) source(ctx context.Context, _ *mcp.CallToolRequest, in sessions.SourceIn) (*mcp.CallToolResult, sessions.SourceOut, error) {
	out, err := sessions.Source(ctx, h.cfg.Roots, in.Agent, in.ID, h.cfg.Now())
	return nil, out, err
}

func (h handlers) deepSourceCheck(ctx context.Context, _ *mcp.CallToolRequest, in sessions.DeepCheckIn) (*mcp.CallToolResult, sessions.DeepCheckOut, error) {
	out, err := sessions.DeepSourceCheck(ctx, h.cfg.Roots, in, h.cfg.Now())
	return nil, out, err
}

func (h handlers) skillsInventory(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SkillsInventory, error) {
	now := h.cfg.Now()
	out := SkillsInventory{SnapshotAt: now.UTC().Format(time.RFC3339), Scope: SkillsScope, Items: []SkillItem{}}
	if h.cfg.Skills != nil {
		if items := h.cfg.Skills(now); items != nil {
			out.Items = items
		}
	}
	return nil, out, nil
}

// stats puts the note about the next page before the aggregate: a client that reads only
// the text content still gets both.
func (h handlers) stats(ctx context.Context, _ *mcp.CallToolRequest, in sessions.StatsIn) (*mcp.CallToolResult, *sessions.Stats, error) {
	st, note, err := sessions.StatsOf(ctx, h.cfg.Roots, in, h.cfg.Now())
	if err != nil || note == "" {
		return nil, st, err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return nil, nil, fmt.Errorf("encode the stats: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: note}, &mcp.TextContent{Text: string(data)}}}, st, nil
}
