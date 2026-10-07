package analytics

import (
	"cmp"
	"slices"
)

// The kinds of a tool in the tools aggregate.
const (
	// ToolKindBuiltin: a tool of the agent itself.
	ToolKindBuiltin = "builtin"
	// ToolKindMCP: a tool of an MCP server.
	ToolKindMCP = "mcp"
)

// ToolCallCounts are the calls of a row in one session, by outcome.
type ToolCallCounts struct {
	Calls  int `json:"calls"`
	Errors int `json:"errors"`
	// Unknown are the calls with no result (StateUnknown).
	Unknown int `json:"unknown"`
}

// add counts one call of state.
func (n *ToolCallCounts) add(state string) {
	n.Calls++
	switch state {
	case StateError:
		n.Errors++
	case StateUnknown:
		n.Unknown++
	}
}

// ToolRow is one tool of the tools aggregate: its calls over the sessions.
type ToolRow struct {
	// Name is the tool as shown (DisplayTool).
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Server is the MCP server of an MCP tool, nil for a builtin one.
	Server *string `json:"server"`
	ToolCallCounts
	// P50S and P95S are the quantiles of the known durations, in seconds; nil without one.
	P50S *float64 `json:"p50_s"`
	P95S *float64 `json:"p95_s"`
	// BySession are the calls by session id; they add up to the row.
	BySession map[string]ToolCallCounts `json:"by_session"`

	durations []float64
}

// MCPRow is one MCP server of the mcp aggregate: the calls of its tools over the sessions.
type MCPRow struct {
	Server string `json:"server"`
	ToolCallCounts
	// AvgS is the mean of the known durations in seconds, to two decimals; nil without one.
	AvgS *float64 `json:"avg_s"`
	// BySession are the calls by session id; they add up to the row.
	BySession map[string]ToolCallCounts `json:"by_session"`

	durations []float64
}

// AggregateTools counts the calls of the sessions by tool, as the colleague's aggregate_tools
// with unknown carried in the row and by session. The commonest tool comes first, a tie by name.
func AggregateTools(sessions []*BuiltSession) []ToolRow {
	rows := map[string]*ToolRow{}
	for _, b := range sessions {
		for i := range b.Calls.Calls {
			c := &b.Calls.Calls[i]
			r, ok := rows[c.Name]
			if !ok {
				r = &ToolRow{Name: c.Name, Kind: ToolKindBuiltin, BySession: map[string]ToolCallCounts{}}
				if c.MCP != "" {
					r.Kind, r.Server = ToolKindMCP, optString(c.MCP)
				}
				rows[c.Name] = r
			}
			r.add(c.State)
			bs := r.BySession[b.Key.SessionID]
			bs.add(c.State)
			r.BySession[b.Key.SessionID] = bs
			if c.DurationS != nil {
				r.durations = append(r.durations, *c.DurationS)
			}
		}
	}
	out := make([]ToolRow, 0, len(rows))
	for _, r := range rows {
		r.P50S, r.P95S = pctOf(r.durations, 0.5), pctOf(r.durations, 0.95)
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b ToolRow) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Name, b.Name))
	})
	return out
}

// AggregateMCP counts the MCP calls of the sessions by server, as the colleague's aggregate_mcp
// with unknown carried in the row and by session. The busiest server comes first, a tie by name.
func AggregateMCP(sessions []*BuiltSession) []MCPRow {
	rows := map[string]*MCPRow{}
	for _, b := range sessions {
		for i := range b.Calls.Calls {
			c := &b.Calls.Calls[i]
			if c.MCP == "" {
				continue
			}
			r, ok := rows[c.MCP]
			if !ok {
				r = &MCPRow{Server: c.MCP, BySession: map[string]ToolCallCounts{}}
				rows[c.MCP] = r
			}
			r.add(c.State)
			bs := r.BySession[b.Key.SessionID]
			bs.add(c.State)
			r.BySession[b.Key.SessionID] = bs
			if c.DurationS != nil {
				r.durations = append(r.durations, *c.DurationS)
			}
		}
	}
	out := make([]MCPRow, 0, len(rows))
	for _, r := range rows {
		if len(r.durations) > 0 {
			sum := 0.0
			for _, d := range r.durations {
				sum += d
			}
			avg := round2(sum / float64(len(r.durations)))
			r.AvgS = &avg
		}
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b MCPRow) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Server, b.Server))
	})
	return out
}

// pctOf is Pct of values, nil when there are none.
func pctOf(values []float64, q float64) *float64 {
	v, ok := Pct(values, q)
	if !ok {
		return nil
	}
	return &v
}
