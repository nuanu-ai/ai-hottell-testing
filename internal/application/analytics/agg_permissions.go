package analytics

// PermissionAskedYou is the row of the questions the agent asked you.
const PermissionAskedYou = "агент спросил вас"

// PermissionRow is one row of the permissions aggregate: what it counts and how often.
type PermissionRow struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	// BySession are the counts by session id; they add up to Count.
	BySession map[string]int `json:"by_session"`
}

// AggregatePermissions counts how the sessions' calls were permitted, as the colleague's
// aggregate_permissions: Claude's tool_decision by decision and source, the permission mode of
// every Codex call, and the questions the agent asked you that were not refused. A
// PermissionRequest hook counts as "<agent> · запрос разрешения", a permission you decided. The
// rows keep the order they first appear in.
func AggregatePermissions(sessions []*BuiltSession) []PermissionRow {
	var rows []*PermissionRow
	byLabel := map[string]*PermissionRow{}
	bump := func(label, sid string, n int) {
		r, ok := byLabel[label]
		if !ok {
			r = &PermissionRow{Label: label, BySession: map[string]int{}}
			byLabel[label] = r
			rows = append(rows, r)
		}
		r.Count += n
		r.BySession[sid] += n
	}
	for _, b := range sessions {
		sid := b.Key.SessionID
		if b.Key.Agent == "claude" {
			for _, e := range b.Claude {
				if e.Event == "tool_decision" {
					bump(decisionLabel(e.Decision, e.Source), sid, 1)
				}
			}
		} else {
			for i := range b.Calls.Calls {
				mode := b.Calls.Calls[i].PermissionMode
				if mode == "" {
					mode = "не указан"
				}
				bump("Codex · режим "+mode, sid, 1)
			}
		}
		if n := countEvents(b.Events, "PermissionRequest"); n > 0 {
			bump(agentLabel(b.Key.Agent)+" · запрос разрешения", sid, n)
		}
		asked := 0
		for _, w := range b.Waits {
			if w.Call != nil && !w.Failed {
				asked++
			}
		}
		if asked > 0 {
			bump(PermissionAskedYou, sid, asked)
		}
	}
	out := make([]PermissionRow, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out
}

// decisionLabel is the row of a Claude tool_decision.
func decisionLabel(decision, source string) string {
	d := decision
	switch decision {
	case "accept":
		d = "разрешено"
	case "reject":
		d = "отклонено"
	case "":
		d = "?"
	}
	s := source
	switch source {
	case "config":
		s = "настройками"
	case "user":
		s = "вами"
	case "hook":
		s = "хуком"
	}
	if s == "" {
		return "Claude · " + d
	}
	return "Claude · " + d + " " + s
}

// agentLabel is the agent's name as shown.
func agentLabel(agent string) string {
	switch agent {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	}
	return agent
}
