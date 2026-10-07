package analytics

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// teamPulseDays are the whole UTC days before today the pulse of «Команда» covers.
	teamPulseDays = 5
	// teamPulseHours are the hours of the pulse, one count each.
	teamPulseHours = teamPulseDays * 24
	// teamPulseTimeout bounds the read of one pulse.
	teamPulseTimeout = 30 * time.Second
)

// TeamPulse is the pulse of «Команда» (HT-540): the hook events of each person by hour over
// [From, To), the 5 whole UTC days before today.
type TeamPulse struct {
	From, To time.Time
	values   map[uuid.UUID][]int
}

// NewTeamPulse is the pulse of [from, to) with the counts of values by person.
func NewTeamPulse(from, to time.Time, values map[uuid.UUID][]int) TeamPulse {
	return TeamPulse{From: from, To: to, values: values}
}

// Of is the person's teamPulseHours counts, oldest first; zeros for a person without an event.
func (p TeamPulse) Of(id uuid.UUID) []int {
	if v, ok := p.values[id]; ok {
		return v
	}
	return make([]int, teamPulseHours)
}

// teamPulseCache holds the pulse of the current UTC day and the read of it in flight; mu guards
// both, and no read of the source happens under it.
type teamPulseCache struct {
	mu     sync.Mutex
	pulse  *TeamPulse
	flight *teamPulseFlight
}

// teamPulseFlight is a read in progress: the calls that come meanwhile wait for done.
type teamPulseFlight struct {
	to   time.Time
	done chan struct{}
	p    TeamPulse
	err  error
}

// TeamPulse returns the pulse of «Команда». The past days do not change, so it is read once a UTC
// day, in one read of the source for everyone, and kept until midnight; the calls that come
// while it is read share that read. A failed read is returned and not kept. The read does not
// take the request's cancellation, so that a closed tab does not fail the calls waiting on it.
func (s *Service) TeamPulse(ctx context.Context) (TeamPulse, error) {
	c := &s.teamPulse
	now := s.clock.Now().UTC()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	c.mu.Lock()
	if c.pulse != nil && c.pulse.To.Equal(to) {
		p := *c.pulse
		c.mu.Unlock()
		return p, nil
	}
	if f := c.flight; f != nil && f.to.Equal(to) {
		c.mu.Unlock()
		<-f.done
		return f.p, f.err
	}
	f := &teamPulseFlight{to: to, done: make(chan struct{})}
	c.flight = f
	c.mu.Unlock()

	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teamPulseTimeout)
	defer cancel()
	from := to.AddDate(0, 0, -teamPulseDays)
	values, err := s.src.ActivityPulse(readCtx, from, to)
	f.p, f.err = NewTeamPulse(from, to, values), err

	c.mu.Lock()
	if err == nil && (c.pulse == nil || !c.pulse.To.After(to)) {
		c.pulse = &f.p
	}
	if c.flight == f {
		c.flight = nil
	}
	c.mu.Unlock()
	close(f.done)
	if err != nil {
		return TeamPulse{}, err
	}
	return f.p, nil
}
