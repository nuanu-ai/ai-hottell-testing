package analytics

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

const (
	// pulseTTL is how long a pulse answers the requests of its filter, error or not.
	pulseTTL = 5 * time.Second
	// pulseTimeout bounds the reads of one pulse.
	pulseTimeout = 4 * time.Second
	// firstPromptMax is the length of an active session's first prompt.
	firstPromptMax = 200
)

// Pulse is the live state of the hooks for the header: the hook events of the last ten minutes
// by 10 seconds and agent, and the sessions active in the last two minutes. On a read failure
// Bars and Active are empty and Error says why.
type Pulse struct {
	At     time.Time     `json:"at"`
	Bars   []PulseBar    `json:"bars"`
	Active []PulseActive `json:"active"`
	Error  string        `json:"error,omitempty"`
}

// PulseBar is the number of hook events of an agent in the 10 seconds from T.
type PulseBar struct {
	T     time.Time `json:"t"`
	Agent string    `json:"agent"`
	N     int       `json:"n"`
}

// PulseActive is a session active now. Project is the last folder of its working directory, ""
// without one; First its first prompt, nil when it has none in the lookback.
type PulseActive struct {
	SID     string    `json:"sid"`
	Agent   string    `json:"agent"`
	UserID  uuid.UUID `json:"user_id"`
	Project string    `json:"project"`
	First   *string   `json:"first,omitempty"`
	LastAt  time.Time `json:"last_at"`
	PerMin  int       `json:"per_min"`
}

// pulseCache holds the latest pulse of each filter, the reads in flight and the first prompts of
// the sessions seen. mu guards the maps only; no read of the source happens under it.
type pulseCache struct {
	mu      sync.Mutex
	pulses  map[string]Pulse
	flights map[string]*pulseFlight
	firsts  map[SessionKey]pulseFirst
}

// pulseFlight is the read of one filter's pulse in progress: the requests of that filter that
// come meanwhile wait for done and take p.
type pulseFlight struct {
	done chan struct{}
	p    Pulse
}

// pulseFirst is a session's first prompt and when the session was last active.
type pulseFirst struct {
	text *string
	seen time.Time
}

// Pulse returns the pulse of the person and agent when given, at most pulseTTL old. The pulse is
// one for every request of a filter: one read at a time per filter, which the requests of that
// filter share, while the pulses of other filters are read alongside (HT-477). The reads do not
// take the request's context, so that a closed tab does not leave «context canceled» in the
// cache. A *domain.InvalidFilterError for an agent other than claude or codex.
func (s *Service) Pulse(userID *uuid.UUID, agent string) (Pulse, error) {
	if agent != "" && agent != "claude" && agent != "codex" {
		return Pulse{}, &domain.InvalidFilterError{Field: "agent", Reason: "claude или codex"}
	}
	q := telemetry.PulseQuery{Agent: agent}
	if userID != nil {
		q.UserID = *userID
	}
	key := q.UserID.String() + "\x00" + agent

	c := &s.pulse
	c.mu.Lock()
	now := s.clock.Now()
	if p, ok := c.pulses[key]; ok && now.Sub(p.At) < pulseTTL {
		c.mu.Unlock()
		return p, nil
	}
	if f, ok := c.flights[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.p, nil
	}
	f := &pulseFlight{done: make(chan struct{})}
	if c.flights == nil {
		c.flights = map[string]*pulseFlight{}
	}
	c.flights[key] = f
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), pulseTimeout)
	defer cancel()
	q.Now = now
	f.p = s.fetchPulse(ctx, q)

	c.mu.Lock()
	if c.pulses == nil {
		c.pulses = map[string]Pulse{}
	}
	c.pulses[key] = f.p
	delete(c.flights, key)
	c.mu.Unlock()
	close(f.done)
	return f.p, nil
}

// fetchPulse reads the pulse of q and the first prompts of its active sessions: the prompts of
// the sessions whose first prompt is not known yet in one read.
func (s *Service) fetchPulse(ctx context.Context, q telemetry.PulseQuery) Pulse {
	raw, err := s.src.Pulse(ctx, q)
	if err != nil {
		return Pulse{At: q.Now, Bars: []PulseBar{}, Active: []PulseActive{}, Error: err.Error()}
	}
	firsts, err := s.firstPrompts(ctx, raw.Active, q.Now)
	if err != nil {
		return Pulse{At: q.Now, Bars: []PulseBar{}, Active: []PulseActive{}, Error: err.Error()}
	}
	p := Pulse{At: q.Now, Bars: []PulseBar{}, Active: []PulseActive{}}
	for _, b := range raw.Bars {
		p.Bars = append(p.Bars, PulseBar{T: b.Start.UTC(), Agent: b.Agent, N: b.Count})
	}
	for i, a := range raw.Active {
		p.Active = append(p.Active, PulseActive{
			SID: a.SessionID, Agent: a.Agent, UserID: a.UserID, Project: projectName(a.Cwd), First: firsts[i],
			LastAt: a.LastAt.UTC(), PerMin: a.PerMinute,
		})
	}
	return p
}

// firstPrompts are the first typed prompts of the active sessions, cleaned to firstPromptMax, in
// their order; nil for a session without one in the lookback. A prompt once found is kept while
// the session stays active; the sessions without a known one are read together, in one read of
// their prompts.
func (s *Service) firstPrompts(
	ctx context.Context, active []telemetry.ActiveSession, now time.Time,
) ([]*string, error) {
	c := &s.pulse
	out := make([]*string, len(active))
	var missing []telemetry.ActiveSession
	c.mu.Lock()
	for i, a := range active {
		if f, ok := c.firsts[activeKey(a)]; ok && f.text != nil {
			out[i] = f.text
		} else {
			missing = append(missing, a)
		}
	}
	c.mu.Unlock()

	found := map[SessionKey]*string{}
	if len(missing) > 0 {
		events, err := s.src.SessionPrompts(ctx, missing, now.Add(-historyLookback), now.Add(time.Nanosecond))
		if err != nil {
			return nil, err
		}
		bySession := map[SessionKey][]telemetry.HookEvent{}
		for _, ev := range events {
			k := SessionKey{UserID: ev.UserID, Agent: ev.Agent, SessionID: ev.SessionID}
			bySession[k] = append(bySession[k], ev)
		}
		for k, evs := range bySession {
			for _, pr := range BuildPrompts(evs) {
				if pr.Kind == PromptKindPrompt {
					t := Clean(pr.Text, firstPromptMax)
					found[k] = &t
					break
				}
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.firsts == nil {
		c.firsts = map[SessionKey]pulseFirst{}
	}
	for i, a := range active {
		k := activeKey(a)
		// A pulse of another filter may have found the prompt meanwhile, or seen the session
		// later: neither is undone.
		prev := c.firsts[k]
		if out[i] == nil {
			out[i] = found[k]
		}
		if out[i] == nil {
			out[i] = prev.text
		}
		c.firsts[k] = pulseFirst{text: out[i], seen: latest(prev.seen, now)}
	}
	for k, f := range c.firsts {
		if now.Sub(f.seen) > telemetry.PulseWindow {
			delete(c.firsts, k)
		}
	}
	return out, nil
}

// activeKey is the session key of an active session.
func activeKey(a telemetry.ActiveSession) SessionKey {
	return SessionKey{UserID: a.UserID, Agent: a.Agent, SessionID: a.SessionID}
}

// latest is the later of a and b.
func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
