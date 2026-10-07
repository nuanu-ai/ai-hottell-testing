package analytics

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// The bounds of the days of a filter. A filter without days covers maxDays: the store keeps no
// longer history the dataset reads.
const (
	minDays = 1
	maxDays = 365
)

// day is the length of a day of a filter's window.
const day = 24 * time.Hour

// Filter is what a dataset is built for. Days is the window [now − Days, now), maxDays when nil;
// a session belongs to the dataset when one of its hook events falls inside it. Agent, Project
// and UserID narrow the sessions when set, Kinds keeps the sessions of these kinds, user,
// automation and agent when empty. Kinds never narrows the cards about the collection and the gaps: they
// count every session, system ones included. Until, when set before now, ends the window in
// its place: the window is [Until − Days, Until) and nothing after Until is read, so a finding
// of a past window keeps the evidence of that window (HT-515). Zone is the zone of the days of
// each session's daily, UTC when nil; it changes no session and no build, only how the days are
// laid out (HT-514).
type Filter struct {
	Days    *int
	Agent   string
	Project string
	UserID  *uuid.UUID
	Kinds   []SessionKind
	Until   *time.Time
	Zone    *time.Location
}

// offsetRe is a zone given as its offset from UTC, +08:00 or -03:30.
var offsetRe = regexp.MustCompile(`^([+-])(\d{2}):(\d{2})$`)

// ParseZone reads the zone of a dataset's days: an IANA name (Asia/Singapore, UTC) or an offset
// ±HH:MM up to 14 hours; nil for "". A *domain.InvalidFilterError names tz otherwise, Local
// included: the server's own zone is no browser's.
func ParseZone(s string) (*time.Location, error) {
	invalid := &domain.InvalidFilterError{Field: "tz", Reason: "имя зоны IANA или смещение ±ЧЧ:ММ"}
	if s == "" {
		return nil, nil
	}
	if m := offsetRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[2])  //nolint:errcheck // two digits by the pattern
		mm, _ := strconv.Atoi(m[3]) //nolint:errcheck // two digits by the pattern
		if h > 14 || mm > 59 || h == 14 && mm > 0 {
			return nil, invalid
		}
		secs := (h*60 + mm) * 60
		if m[1] == "-" {
			secs = -secs
		}
		return time.FixedZone(s, secs), nil
	}
	if s == "Local" {
		return nil, invalid
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return nil, invalid
	}
	return loc, nil
}

// DefaultKinds are the kinds a filter keeps when it names none.
func DefaultKinds() []SessionKind {
	return []SessionKind{SessionKindUser, SessionKindAutomation, SessionKindAgent}
}

// Validate checks the values: days from 1 to 365, agent claude or codex, each kind user,
// automation, agent or system. The error is a *domain.InvalidFilterError naming the parameter.
func (f Filter) Validate() error {
	if f.Days != nil && (*f.Days < minDays || *f.Days > maxDays) {
		return &domain.InvalidFilterError{Field: "days", Reason: "от 1 до 365"}
	}
	if f.Agent != "" && f.Agent != "claude" && f.Agent != "codex" {
		return &domain.InvalidFilterError{Field: "agent", Reason: "claude или codex"}
	}
	for _, k := range f.Kinds {
		if k != SessionKindUser && k != SessionKindAutomation && k != SessionKindAgent && k != SessionKindSystem {
			return &domain.InvalidFilterError{Field: "kind", Reason: "user, automation, agent или system"}
		}
	}
	return nil
}

// Window is the window of the filter at now: it ends at Until when that is before now.
func (f Filter) Window(now time.Time) Window {
	if f.Until != nil && f.Until.Before(now) {
		now = *f.Until
	}
	days := maxDays
	if f.Days != nil {
		days = *f.Days
	}
	now = now.UTC()
	return Window{From: now.Add(-time.Duration(days) * day), To: now}
}

// withDefaults is the filter as the dataset echoes it: the days and the kinds filled in.
func (f Filter) withDefaults() Filter {
	if f.Days == nil {
		d := maxDays
		f.Days = &d
	}
	if len(f.Kinds) == 0 {
		f.Kinds = DefaultKinds()
	}
	return f
}

// inScope tells whether the session passes the agent, project and person of the filter.
func (f Filter) inScope(s *SessionBuild) bool {
	return (f.Agent == "" || s.Key.Agent == f.Agent) &&
		(f.Project == "" || s.Project == f.Project) &&
		(f.UserID == nil || s.Key.UserID == *f.UserID)
}

// keeps tells whether the filter shows the session: in scope and of one of its kinds.
func (f Filter) keeps(s *SessionBuild) bool {
	return f.inScope(s) && slices.Contains(f.withDefaults().Kinds, s.Kind)
}

// UserRef is a person of the facets; Name is filled in by the service.
type UserRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Facets are the values the filter can take in the window: over the sessions of the window,
// before the agent, project, person and kind narrow them.
type Facets struct {
	Agents   []string      `json:"agents"`
	Projects []string      `json:"projects"`
	Users    []UserRef     `json:"users"`
	Kinds    []SessionKind `json:"kinds"`
}

// buildFacets lists the agents, projects, people and kinds of the sessions, each sorted.
func buildFacets(sessions []SessionBuild) Facets {
	fc := Facets{Agents: []string{}, Projects: []string{}, Users: []UserRef{}, Kinds: []SessionKind{}}
	for i := range sessions {
		s := &sessions[i]
		if !slices.Contains(fc.Agents, s.Key.Agent) {
			fc.Agents = append(fc.Agents, s.Key.Agent)
		}
		if s.Project != "" && !slices.Contains(fc.Projects, s.Project) {
			fc.Projects = append(fc.Projects, s.Project)
		}
		if !slices.ContainsFunc(fc.Users, func(u UserRef) bool { return u.ID == s.Key.UserID }) {
			fc.Users = append(fc.Users, UserRef{ID: s.Key.UserID})
		}
		if !slices.Contains(fc.Kinds, s.Kind) {
			fc.Kinds = append(fc.Kinds, s.Kind)
		}
	}
	slices.Sort(fc.Agents)
	slices.Sort(fc.Projects)
	slices.SortFunc(fc.Users, func(a, b UserRef) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	slices.Sort(fc.Kinds)
	return fc
}
