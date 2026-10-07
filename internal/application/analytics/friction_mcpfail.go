package analytics

// The thresholds of the mcpfail signal.
const (
	// MCPFailMin is the fewest errors of one MCP server in a session that make an episode, as the
	// colleague's builder names it.
	MCPFailMin = 3
	// mcpFailEvidenceMax is how many of a server's errors an episode keeps: the latest.
	mcpFailEvidenceMax = 8
)

// DetectMCPFail finds the MCP servers that kept failing in a session: one episode for each
// server with at least MCPFailMin failed calls, in the order the servers first failed. An
// episode keeps the latest mcpFailEvidenceMax errors, oldest first. calls are the session's calls
// in their order, sid its id.
func DetectMCPFail(sid string, calls []Call) []FrictionEpisode {
	var servers []string
	byServer := map[string][]Call{}
	for _, c := range calls {
		if c.MCP == "" || c.State != StateError {
			continue
		}
		if _, seen := byServer[c.MCP]; !seen {
			servers = append(servers, c.MCP)
		}
		byServer[c.MCP] = append(byServer[c.MCP], c)
	}
	var out []FrictionEpisode
	for _, server := range servers {
		items := byServer[server]
		if len(items) < MCPFailMin {
			continue
		}
		var ep FrictionEpisode
		for _, x := range items[max(0, len(items)-mcpFailEvidenceMax):] {
			ep.Evidence = append(ep.Evidence, frictionEvidence(sid, ISO(x.At), x.Name+" · "+errorNote(x)))
		}
		out = append(out, ep)
	}
	return out
}
