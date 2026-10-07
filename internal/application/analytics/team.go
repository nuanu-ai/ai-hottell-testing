package analytics

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// NoProject names the sessions without a project in the filter of projects, as the page does.
const NoProject = "без проекта"

// TeamFilter is what «Команда» asks for: the period and the choice of sessions in it. Days is
// maxDays when nil; Agent and Project choose nothing when empty; AllKinds keeps the service
// sessions, which are left out by default.
type TeamFilter struct {
	Days     *int
	Agent    string
	Project  string
	AllKinds bool
}

// Team is «Команда» for a period (HT-531): each person's numbers over their chosen sessions, and
// what the filters of the page need. Unknown is nil, never 0.
type Team struct {
	GeneratedAt time.Time
	// Window is the period: the days before the dataset's end, or the dataset's window for «Всё».
	Window Window
	// People are the people with a chosen session, in no order.
	People []TeamPerson
	// Projects are the projects of every session of the dataset (twice the period), whatever the
	// choice, as the page's filter offered them; NoProject stands for the sessions without one.
	Projects []string
	// HasSystem tells that a session of the period is a service one.
	HasSystem bool
	// Gaps are the dataset's notes that are not about one session; SessionGaps counts the rest.
	Gaps        []string
	SessionGaps int
}

// TeamPerson is one person of «Команда»: the numbers «Обзор» shows over their chosen sessions.
type TeamPerson struct {
	UserID   uuid.UUID
	UserName string
	// Sessions counts the chosen sessions of kind user.
	Sessions int
	// UserMin and AgentMin sum the person's and the agent's minutes over the sessions that are
	// neither automation nor agent; nil without such a session.
	UserMin  *float64
	AgentMin *float64
	// CostUSD sums the known costs, nil when no session has one; CostPartial tells that some
	// session has none, so the sum is a lower bound.
	CostUSD     *float64
	CostPartial bool
	// ErrorRate is the tool errors over the calls with a known outcome; nil without such calls.
	ErrorRate *float64
	// FrictionSessions counts the sessions with a friction signal.
	FrictionSessions int
	// LastActive is the latest end, or start without one, of the sessions.
	LastActive time.Time
}

// teamKinds are the kinds the dataset of «Команда» keeps: all of them, chosen afterwards.
func teamKinds() []SessionKind {
	return []SessionKind{SessionKindSystem, SessionKindAgent, SessionKindAutomation, SessionKindUser}
}

// Team returns «Команда» for the filter, folded from the dataset of everyone over twice the period
// (cachedDataset), as the page reads it: a session's numbers rest on the same history as in
// «Обзор», and one build serves every agent, project and kind. A *domain.InvalidFilterError when a
// value of the filter is out of range.
func (s *Service) Team(ctx context.Context, f TeamFilter) (Team, error) {
	if f.Days != nil && (*f.Days < minDays || *f.Days > maxDays) {
		return Team{}, Filter{Days: f.Days}.Validate()
	}
	days := maxDays
	if f.Days != nil {
		days = min(2**f.Days, maxDays)
	}
	ds, err := s.cachedDataset(ctx, Filter{Days: &days, Kinds: teamKinds()})
	if err != nil {
		return Team{}, err
	}
	return BuildTeam(ds, f), nil
}

// BuildTeam folds the dataset of everyone into «Команда» for a period ending at the dataset's end,
// as the page counted it from the sessions (HT-435, HT-466): with days, a session is in the period
// when it ends (or starts, without an end) after its start and starts no later than its end, and
// without them («Всё») every session of the dataset is; then the agent, the project and the kind
// choose. The numbers of a person are those of «Обзор» over their chosen sessions.
func BuildTeam(ds Dataset, f TeamFilter) Team {
	team := Team{
		GeneratedAt: ds.GeneratedAt, Window: ds.Window, People: []TeamPerson{}, Projects: []string{},
		Gaps: []string{},
	}
	if f.Days != nil {
		team.Window.From = ds.Window.To.Add(-time.Duration(*f.Days) * 24 * time.Hour)
	}
	projects := map[string]bool{}
	for i := range ds.Sessions {
		s := &ds.Sessions[i]
		if p := projectOf(s); !projects[p] {
			projects[p] = true
			team.Projects = append(team.Projects, p)
		}
		team.HasSystem = team.HasSystem || s.Kind == SessionKindSystem
	}
	for _, gap := range ds.Gaps {
		if sessionGap.MatchString(gap) {
			team.SessionGaps++
		} else {
			team.Gaps = append(team.Gaps, gap)
		}
	}

	byPerson := map[uuid.UUID][]*Session{}
	var order []uuid.UUID
	for i := range ds.Sessions {
		s := &ds.Sessions[i]
		if (f.Days != nil && !inTeamPeriod(s, ds.Window.To, *f.Days)) || !f.chooses(s) {
			continue
		}
		if _, seen := byPerson[s.UserID]; !seen {
			order = append(order, s.UserID)
		}
		byPerson[s.UserID] = append(byPerson[s.UserID], s)
	}
	for _, id := range order {
		team.People = append(team.People, teamPerson(byPerson[id]))
	}
	return team
}

// sessionGap tells a note about one session: it starts with the session's short id, as the page
// tells them apart.
var sessionGap = regexp.MustCompile(`^[0-9a-f]{8}(…[0-9a-f]{4})?:\s*`)

// projectOf is the session's project for the filter of projects.
func projectOf(s *Session) string {
	if s.Project == "" {
		return NoProject
	}
	return s.Project
}

// inTeamPeriod tells whether the session touches the days before to, by the page's rule: it ends
// (or starts, without an end) after the period's start and starts no later than its end.
func inTeamPeriod(s *Session, to time.Time, days int) bool {
	last := s.End
	if last == "" {
		last = s.Start
	}
	end, errEnd := time.Parse(time.RFC3339Nano, last)
	start, errStart := time.Parse(time.RFC3339Nano, s.Start)
	from := to.Add(-time.Duration(days) * 24 * time.Hour)
	return errEnd == nil && errStart == nil && end.After(from) && !start.After(to)
}

// chooses tells whether the filter keeps the session.
func (f TeamFilter) chooses(s *Session) bool {
	return (f.Agent == "" || s.Agent == f.Agent) &&
		(f.Project == "" || projectOf(s) == f.Project) &&
		(f.AllKinds || s.Kind != SessionKindSystem)
}

// teamPerson counts one person's numbers over their chosen sessions, at least one.
func teamPerson(sessions []*Session) TeamPerson {
	p := TeamPerson{UserID: sessions[0].UserID, UserName: sessions[0].UserName}
	var userMin, agentMin, cost float64
	own, costed, errors, known := 0, 0, 0, 0
	for _, s := range sessions {
		if s.Kind == SessionKindUser {
			p.Sessions++
		}
		if s.Kind != SessionKindAutomation && s.Kind != SessionKindAgent {
			own++
			userMin += s.UserMin
			agentMin += s.ActiveMin
		}
		if s.CostUSD != nil {
			costed++
			cost += *s.CostUSD
		}
		errors += s.Errors
		known += s.OutcomeKnown
		if len(s.Flags) > 0 {
			p.FrictionSessions++
		}
		if at := lastActive(s); at.After(p.LastActive) {
			p.LastActive = at
		}
	}
	if own > 0 {
		p.UserMin, p.AgentMin = &userMin, &agentMin
	}
	if costed > 0 {
		p.CostUSD = &cost
		p.CostPartial = costed < len(sessions)
	}
	if known > 0 {
		rate := float64(errors) / float64(known)
		p.ErrorRate = &rate
	}
	return p
}

// lastActive is the session's end, or its start without one; zero when neither reads.
func lastActive(s *Session) time.Time {
	for _, v := range []string{s.End, s.Start} {
		if at, err := time.Parse(time.RFC3339Nano, v); v != "" && err == nil {
			return at
		}
	}
	return time.Time{}
}
