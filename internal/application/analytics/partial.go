package analytics

import (
	"slices"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// partialAfter is how long after the instant of its UUID v7 id a session may send its first hook
// and still count as recorded from its start.
const partialAfter = time.Hour

// gapTimeLayout is a time in a line of the gaps.
const gapTimeLayout = "2006-01-02 15:04 UTC"

// Partial tells whether the hooks recorded a session only from somewhere in its middle.
type Partial struct {
	Partial bool
	// FirstHook is the session's first hook event; Created the instant of its UUID v7 id, zero
	// for another id; FirstRecord the session's first transcript or native OpenTelemetry record,
	// zero without one.
	FirstHook, Created, FirstRecord time.Time
}

// PartialOf reads whether a session's events, oldest first, start mid-session: its first hook
// comes more than partialAfter after the instant of its UUID v7 id, or its first SessionStart of
// source startup, clear or resume is a resume. Codex Desktop and Claude Code send resume also on
// a return to a session recorded from its first event, so a resume after a startup or a clear (a
// new session in Claude Code) is a recorded session; compact is the middle of a session and is
// skipped.
//
// A session without a UUID v7 id (Claude's UUID v4) and without any SessionStart has no instant
// of creation: it is partial when its first hook comes more than partialAfter after firstRecord,
// its first transcript or native OpenTelemetry record; without that record, when its first hook
// is not a UserPromptSubmit.
func PartialOf(sid string, events []telemetry.HookEvent, firstRecord time.Time) Partial {
	p := Partial{FirstRecord: firstRecord}
	if len(events) > 0 {
		p.FirstHook = events[0].Time
	}
	p.Created, _ = UUID7Time(sid)
	late := !p.Created.IsZero() && !p.FirstHook.IsZero() && p.FirstHook.Sub(p.Created) > partialAfter
	p.Partial = late || resumedWithoutStart(events) || p.Created.IsZero() && tailWithoutStart(events, firstRecord)
	return p
}

// tailWithoutStart tells whether a session without any SessionStart was recorded from its middle:
// its first hook comes more than partialAfter after firstRecord, or, without firstRecord, its
// first hook is not a UserPromptSubmit.
func tailWithoutStart(events []telemetry.HookEvent, firstRecord time.Time) bool {
	if len(events) == 0 || slices.ContainsFunc(events, func(ev telemetry.HookEvent) bool { return ev.Event == "SessionStart" }) {
		return false
	}
	if !firstRecord.IsZero() {
		return events[0].Time.Sub(firstRecord) > partialAfter
	}
	return events[0].Event != "UserPromptSubmit"
}

// HooksSource is the state of the hooks of the session: "partial" or "recorded".
func (p Partial) HooksSource() string {
	if p.Partial {
		return "partial"
	}
	return SourceRecorded
}

// PartialGap is the line of the gaps for a session only the tail of which is recorded.
func PartialGap(sid string, p Partial) (string, bool) {
	if !p.Partial {
		return "", false
	}
	since, created := "?", "раньше"
	if !p.FirstHook.IsZero() {
		since = p.FirstHook.UTC().Format(gapTimeLayout)
	}
	if !p.Created.IsZero() {
		created = p.Created.UTC().Format(gapTimeLayout)
	} else if !p.FirstRecord.IsZero() {
		return ShortID(sid) + ": записан только хвост с " + since + " — первая запись сессии " +
			p.FirstRecord.UTC().Format(gapTimeLayout) + ", хуков тогда не было или она продолжена (resume).", true
	}
	return ShortID(sid) + ": записан только хвост с " + since + " — сессия создана " + created +
		", хуков тогда не было или она продолжена (resume).", true
}

// resumedWithoutStart tells whether the first SessionStart of source startup, clear or resume is
// a resume.
func resumedWithoutStart(events []telemetry.HookEvent) bool {
	starts := slices.DeleteFunc(slices.Clone(events), func(ev telemetry.HookEvent) bool { return ev.Event != "SessionStart" })
	slices.SortStableFunc(starts, func(a, b telemetry.HookEvent) int { return a.Time.Compare(b.Time) })
	for _, ev := range starts {
		switch ev.Source {
		case "startup", "clear":
			return false
		case "resume":
			return true
		}
	}
	return false
}
