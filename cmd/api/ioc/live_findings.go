package ioc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/analytics"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// liveAnalytics is the part of the analytics service the live findings read.
type liveAnalytics interface {
	Dataset(ctx context.Context, f analytics.Filter) (analytics.Dataset, error)
	Session(ctx context.Context, sid, agent string, userID *uuid.UUID) (analytics.SessionTimeline, error)
}

// maxLiveDays is the longest window the analytics builds, the one of findings without since.
const maxLiveDays = 365

// liveFindings is the port E2 of the MCP findings over the analytics the dashboard reads: the
// same filter the dashboard sends for a person (its default kinds), so the ids match the cards.
type liveFindings struct {
	analytics liveAnalytics
	now       func() time.Time
}

// Live builds userID's dataset over the whole days from from back from to, or from now when to
// is zero or later, and returns its detector cards and friction rows. A to before now ends the
// dataset's window there (analytics.Filter.Until).
func (l liveFindings) Live(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]mcp.LiveFinding, error) {
	f := analytics.Filter{UserID: &userID, Kinds: analytics.DefaultKinds()}
	end := l.now()
	if !to.IsZero() && to.Before(end) {
		end = to
		f.Until = &end
	}
	days := maxLiveDays
	if !from.IsZero() {
		days = min(maxLiveDays, max(1, int(math.Ceil(end.Sub(from).Hours()/24))))
	}
	f.Days = &days
	ds, err := l.analytics.Dataset(ctx, f)
	if errors.Is(err, domain.ErrAnalyticsUnavailable) {
		return nil, mcp.ErrLiveUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("build the analytics: %w", err)
	}
	out := make([]mcp.LiveFinding, 0, len(ds.Findings)+len(ds.Friction))
	for _, f := range ds.Findings {
		out = append(out, mcp.LiveFinding{
			ID: f.ID, Title: f.Title, Kind: string(f.Kind), Pattern: f.PatternID,
			Readiness: string(f.Readiness), Decision: string(f.Decision), Execution: string(f.Execution),
			Effect: string(f.Effect), Sessions: f.Sessions, Evidence: liveEvidence(f.Ev),
		})
	}
	// A friction row is a card of the dashboard's friction table: its id is the one the
	// dashboard links the coach with.
	for _, r := range ds.Friction {
		if len(r.Sessions) == 0 {
			continue // the aggregate lists every signal, observed or not
		}
		out = append(out, mcp.LiveFinding{
			ID: "friction:" + r.Key, Title: r.Name, Kind: "friction", Pattern: r.Key,
			Readiness: string(analytics.ReadinessHypothesis), Sessions: r.Sessions, Evidence: liveEvidence(r.Evidence),
		})
	}
	return out, nil
}

// liveEvidence carries the evidence over; an unreadable time is unknown.
func liveEvidence(ev []analytics.Evidence) []mcp.LiveEvidence {
	out := make([]mcp.LiveEvidence, 0, len(ev))
	for _, e := range ev {
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			at = time.Time{}
		}
		out = append(out, mcp.LiveEvidence{Session: e.SID, Line: e.Line, At: at, Text: e.Text})
	}
	return out
}

// SourceLines reads userID's session timeline and maps each instant to the transcript line its
// events are built from. An instant whose events come from several lines, or also from a
// subagent's transcript, is left out: the time cannot tell which event the evidence is, and a
// wrong line would point the coach at another event.
func (l liveFindings) SourceLines(ctx context.Context, userID uuid.UUID, session string) (map[int64]int, error) {
	tl, err := l.analytics.Session(ctx, session, "", &userID)
	if err != nil {
		return nil, fmt.Errorf("read the session timeline: %w", err)
	}
	lines := make(map[int64]int, len(tl.Events))
	foreign := map[int64]bool{}
	for _, e := range tl.Events {
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			continue
		}
		ms := at.UnixMilli()
		switch {
		case e.SrcLine == nil:
		case e.SrcKind != nil && *e.SrcKind != analytics.SrcKindMain:
			foreign[ms] = true
		default:
			if line, seen := lines[ms]; seen && line != *e.SrcLine {
				foreign[ms] = true // two transcript lines of one instant: the time cannot tell them apart
			} else {
				lines[ms] = *e.SrcLine
			}
		}
	}
	for ms := range foreign {
		delete(lines, ms)
	}
	return lines, nil
}
