package analytics

import (
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// The reasons a hook event is dropped before the sessions are built, as the gaps of the dataset
// name them.
const (
	// DropNoAgent: the agent is neither claude nor codex, such as the install check's "install".
	DropNoAgent = "агент не указан"
	// DropNoSessionID: the event carries no session id.
	DropNoSessionID = "без session_id"
	// DropHottellCheck: the session id is a synthetic check of hottell (hottell-…-check-…).
	DropHottellCheck = "проверки hottell"
	// DropNoTime: the event carries no time.
	DropNoTime = "без времени"
)

// syntheticSIDRe matches the session ids hottell's own checks send under.
var syntheticSIDRe = regexp.MustCompile(`^hottell-.*-check-`)

// SessionKey identifies a session: the person, the agent and the session id together.
type SessionKey struct {
	UserID    uuid.UUID
	Agent     string
	SessionID string
}

// Grouped are the hook events of a period laid out by session.
type Grouped struct {
	// Sessions are the events of each session that did work, oldest first.
	Sessions map[SessionKey][]telemetry.HookEvent
	// Empty are the sessions with no prompt and no tool call, in the order they first appear.
	Empty []SessionKey
	// Dropped counts the events dropped by the reason (DropNoAgent, DropNoSessionID,
	// DropHottellCheck, DropNoTime); a reason with no event is absent.
	Dropped map[string]int
}

// GroupSessions lays the hook events out by SessionKey. An event of an agent other than claude or
// codex, without a session id, of a synthetic hottell check or without a time is dropped and
// counted. A session without a UserPromptSubmit, PreToolUse, PostToolUse or PostToolUseFailure
// did no work and goes to Empty.
func GroupSessions(events []telemetry.HookEvent) Grouped {
	var l layout
	for i := range events {
		l.add(events[i])
	}
	return l.grouped()
}

// layout lays hook events out by session as they come, so that they need not be held anywhere
// else first: a slice of the whole read beside the layout held the period's events twice (HT-533).
type layout struct {
	g     Grouped
	order []SessionKey
}

// add lays ev out in its session, or counts it as dropped.
func (l *layout) add(ev telemetry.HookEvent) {
	if l.g.Sessions == nil {
		l.g = Grouped{Sessions: map[SessionKey][]telemetry.HookEvent{}, Dropped: map[string]int{}}
	}
	if reason := dropReason(ev); reason != "" {
		l.g.Dropped[reason]++
		return
	}
	key := SessionKey{UserID: ev.UserID, Agent: ev.Agent, SessionID: ev.SessionID}
	evs, seen := l.g.Sessions[key]
	if !seen {
		l.order = append(l.order, key)
	}
	l.g.Sessions[key] = append(evs, ev)
}

// grouped is what GroupSessions gives of the events added: each session oldest first and at its
// exact size, the sessions that did no work in Empty in the order they first came. A slice grown
// by append keeps up to twice its events' room for as long as the dataset lives (HT-529), so each
// session is copied to its size, one at a time: the room of all of them at once is what a count
// of every session before laying them out would save, at a query that reads every record's
// attributes once more (HT-533).
func (l *layout) grouped() Grouped {
	if l.g.Sessions == nil {
		return Grouped{Sessions: map[SessionKey][]telemetry.HookEvent{}, Dropped: map[string]int{}}
	}
	for _, key := range l.order {
		evs := l.g.Sessions[key]
		if cap(evs) != len(evs) {
			evs = append(make([]telemetry.HookEvent, 0, len(evs)), evs...)
			l.g.Sessions[key] = evs
		}
		slices.SortStableFunc(evs, func(a, b telemetry.HookEvent) int { return a.Time.Compare(b.Time) })
		if !slices.ContainsFunc(evs, isWorkEvent) {
			delete(l.g.Sessions, key)
			l.g.Empty = append(l.g.Empty, key)
		}
	}
	return l.g
}

// dropReason is why ev is dropped, or "" when it is kept.
func dropReason(ev telemetry.HookEvent) string {
	switch {
	case ev.Agent != "claude" && ev.Agent != "codex":
		return DropNoAgent
	case ev.SessionID == "":
		return DropNoSessionID
	case syntheticSIDRe.MatchString(ev.SessionID):
		return DropHottellCheck
	case ev.Time.IsZero():
		return DropNoTime
	}
	return ""
}

// isWorkEvent tells the events that show a session did work: a prompt or a tool call.
func isWorkEvent(ev telemetry.HookEvent) bool {
	switch ev.Event {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure":
		return true
	}
	return false
}

// UUID7Time is the instant a UUID v7 session id carries, in UTC; false for any other id.
func UUID7Time(sid string) (time.Time, bool) {
	h := strings.ReplaceAll(sid, "-", "")
	if len(h) != 32 || h[12] != '7' {
		return time.Time{}, false
	}
	if _, err := hex.DecodeString(h); err != nil {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(h[:12], 16, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

// MCPServerOf is the MCP server of a call, or "" when it has none. Claude sends the server as
// the JSON object {"name": …, "source": …}, whose name is the server; another non-empty attr is
// the server as it is. Codex only has the tool name mcp__<server>__<tool>.
func MCPServerOf(tool, attr string) string {
	if attr != "" {
		if strings.HasPrefix(strings.TrimLeft(attr, " \t\r\n"), "{") {
			var obj map[string]any
			if json.Unmarshal([]byte(attr), &obj) == nil {
				if name := firstString(obj, "name"); name != "" {
					return name
				}
			}
		}
		return attr
	}
	if strings.HasPrefix(tool, "mcp__") {
		if parts := strings.Split(tool, "__"); len(parts) > 2 {
			return parts[1]
		}
	}
	return ""
}
