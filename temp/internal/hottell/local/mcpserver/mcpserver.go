// Package mcpserver is hottell-local: the MCP server the agents start from the hottell
// binary over stdio. It gives the hottell-coach skill this Mac's sessions, the dashboard's
// findings and the coach's journal; nothing it reads leaves the machine. The service's
// server is hottell; this one never talks to it.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/coach"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

// Name is the server's name in the agents' configs.
const Name = "hottell-local"

// Config is what the server reads.
type Config struct {
	Roots sessions.Roots
	// DataDir is the dashboard's local-data: ui-live/, ui/, reports/v2/, coach/.
	DataDir string
	Version string
	Now     func() time.Time
}

// New returns the server with its tools.
func New(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: cfg.Version}, nil)
	h := handlers{cfg: cfg, journal: coach.NewJournal(filepath.Join(cfg.DataDir, "coach", "journal.jsonl"))}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "sessions_list",
		Description: "Inventory of Claude Code and Codex transcripts on this Mac: id, agent, project (cwd), size, times. since/until look at the file's mtime, not at the events; offset/limit page the list.",
	}, h.list)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "session_read",
		Description: "One session page by page: user and agent turns, reasoning, tool calls and results, tokens. from/to keep only events of that time; matched_events and out_of_period count the whole session, next_offset is the next page (0 — none).",
	}, h.read)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "session_stats",
		Description: "Aggregates of one session (id) or of a period: events by kind, tools and their errors, tokens by model, turns, duration. from/to count only events of that time; coverage says how many sessions and events were seen of those found, coverage.next_offset is the next page. A page may hold fewer sessions than limit: it stops at a byte budget of transcripts.",
	}, h.stats)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "findings",
		Description: "Findings of the hottell dashboard for a period: the live dataset (findings, friction, skills, service sessions) and the reviewed one (Deep 2.0 and the proposal registry). A finding counts for the period only with evidence timed inside it; findings whose evidence has no time are listed apart in undated. The evidence list keeps the newest 3 of evidence_total; an evidence line is a dashboard or rollout line, not a seq.",
	}, h.findings)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "coach_journal",
		Description: "Journal of hottell-coach. op=read — all decisions or one by id/topic_key, each with its latest check folded in (result, observations, repeats, checked_at, checks). op=append — one record: a decision on a topic (applied, declined, not_justified, test) or a check of a change (check_of). Secrets and the home directory are masked; quotes are at most 200 characters.",
	}, h.coachJournal)
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
	cfg     Config
	journal *coach.Journal
}

type listOut struct {
	Total    int             `json:"total"`
	Returned int             `json:"returned"`
	Sessions []sessions.Info `json:"sessions"`
}

func (h handlers) list(_ context.Context, _ *mcp.CallToolRequest, in sessions.Filter) (*mcp.CallToolResult, listOut, error) {
	list, total, err := sessions.List(h.cfg.Roots, in, h.cfg.Now())
	if err != nil {
		return nil, listOut{}, err
	}
	if list == nil {
		list = []sessions.Info{}
	}
	return nil, listOut{Total: total, Returned: len(list), Sessions: list}, nil
}

func (h handlers) read(_ context.Context, _ *mcp.CallToolRequest, in sessions.ReadIn) (*mcp.CallToolResult, sessions.ReadOut, error) {
	out, err := sessions.Read(h.cfg.Roots, in, h.cfg.Now())
	return nil, out, err
}

// stats puts the note about the next page before the aggregate: a client that reads only
// the text content still gets both.
func (h handlers) stats(_ context.Context, _ *mcp.CallToolRequest, in sessions.StatsIn) (*mcp.CallToolResult, *sessions.Stats, error) {
	st, note, err := sessions.StatsOf(h.cfg.Roots, in, h.cfg.Now())
	if err != nil || note == "" {
		return nil, st, err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return nil, nil, fmt.Errorf("encode the stats: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: note}, &mcp.TextContent{Text: string(data)}}}, st, nil
}

func (h handlers) findings(_ context.Context, _ *mcp.CallToolRequest, in coach.FindingsIn) (*mcp.CallToolResult, coach.FindingsOut, error) {
	out, err := coach.Findings(h.cfg.DataDir, in, h.cfg.Now())
	return nil, out, err
}

// journalIn holds the record as a map, not a struct: a record holds nulls (layer, result)
// and the journal itself checks every field strictly.
type journalIn struct {
	Op       string         `json:"op" jsonschema:"read or append"`
	ID       string         `json:"id,omitempty" jsonschema:"read: one decision"`
	TopicKey string         `json:"topic_key,omitempty" jsonschema:"read: the decisions of one topic"`
	Entry    map[string]any `json:"entry,omitempty" jsonschema:"append: one record — a decision or a check (check_of); fields as analytics/coach/SKILL.md, section Журнал, lists them"`
}

type journalOut struct {
	Path    string           `json:"path"`
	Count   int              `json:"count"`
	Entries []map[string]any `json:"entries"`
}

func (h handlers) coachJournal(_ context.Context, _ *mcp.CallToolRequest, in journalIn) (*mcp.CallToolResult, journalOut, error) {
	out := journalOut{Path: h.journal.Path(), Entries: []map[string]any{}}
	switch in.Op {
	case "read":
		entries, err := h.journal.Read(in.ID, in.TopicKey)
		if err != nil {
			return nil, out, err
		}
		if entries != nil {
			out.Entries = entries
		}
	case "append":
		rec, err := h.journal.Append(in.Entry, h.cfg.Now())
		if err != nil {
			return nil, out, err
		}
		out.Entries = append(out.Entries, rec)
	default:
		return nil, out, fmt.Errorf("op: read or append, not %q", in.Op)
	}
	out.Count = len(out.Entries)
	return nil, out, nil
}
