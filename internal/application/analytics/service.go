package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
	"git.alva.dev/alva/harness-telemetry/pkg/redact"
)

const (
	// cacheTTL is how long a built dataset answers the requests of its filter as it is. Every
	// period is cut from one build of maxDays, which reads the whole year: five minutes, not one,
	// between its reads (HT-536).
	cacheTTL = 5 * time.Minute
	// staleMax is how long past its build a dataset still answers at once while a build in the
	// background replaces it: the numbers of the dashboard may be up to ten minutes old, the
	// live ones are in the pulse and the live session's timeline (HT-469).
	staleMax = 10 * time.Minute
	// buildTimeout bounds one build of a dataset. A build does not take the cancellation of the
	// request that started it, so that the request going away does not cancel it for the
	// requests that wait for it (HT-433); this bound stops a build that would never end.
	buildTimeout = 5 * time.Minute
	// maxCacheEntries bounds the finished datasets kept at once; the oldest goes first.
	maxCacheEntries = 16
	// maxViews bounds the datasets of filters kept beside one window's build; past it they are
	// cut again on request.
	maxViews = 32
	// maxCachedSessions bounds the sessions the finished datasets hold together: a dataset keeps
	// every session as it was built, its events and calls, and its memory grows with them, not
	// with the number of filters (HT-529). The oldest goes first.
	maxCachedSessions = 2000
	// maxBuilds bounds the datasets built at once, of every filter together: one build holds
	// about a gigabyte on the stand, and several at once ran the app out of memory (HT-525).
	maxBuilds = 1
)

// Service builds the live analytics on request. A dataset is built over every person's sessions
// and is heavy, so the one of a filter answers as it is for cacheTTL after its build ended; up
// to staleMax it still answers at once, and one build in the background replaces it. The
// requests of a filter that come while it is first being built wait for that build instead of
// starting their own. A build runs apart from the requests: one that goes away stops waiting,
// and the build goes on.
type Service struct {
	src   Source
	users Users
	clock Clock

	mu      sync.Mutex
	entries map[string]*cacheEntry
	// sessionBudget is maxCachedSessions, which the tests lower.
	sessionBudget int
	// cuts counts the datasets cut from window builds.
	cuts atomic.Int64
	// refreshes are the builds running in the background.
	refreshes sync.WaitGroup
	// builds holds a slot for each build running, up to maxBuilds.
	builds chan struct{}

	pulse     pulseCache
	teamPulse teamPulseCache
	delivery  DeliveryPorts
	hidden    HiddenTopicsStore
}

// cacheEntry is the build of one window (HT-527): done closes when the build ends, and built is
// when it ended. The dataset of each filter over the window is cut from it once and kept in views.
type cacheEntry struct {
	done   chan struct{}
	built  time.Time
	period *periodBuild
	// names are the names of the window's people.
	names map[uuid.UUID]string
	err   error
	// refresh is the build in the background that replaces the entry, nil when none runs;
	// guarded by Service.mu.
	refresh *cacheEntry
	// sessions are the places of the sessions in period.sessions, by key; set with period.
	sessions map[SessionKey]int
	// views are the datasets cut from period, by the filter's cacheKey; guarded by Service.mu.
	views map[string]*viewEntry
}

// viewEntry is the dataset of one filter cut from a window's build; done closes once ds is set.
type viewEntry struct {
	done chan struct{}
	ds   Dataset
	err  error
}

// NewService returns a Service on its ports; the options add the optional ones.
func NewService(src Source, users Users, clock Clock, opts ...Option) *Service {
	s := &Service{
		src: src, users: users, clock: clock, entries: map[string]*cacheEntry{},
		sessionBudget: maxCachedSessions, builds: make(chan struct{}, maxBuilds),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Dataset returns the dataset of the filter (cachedDataset) with the daily of its sessions laid
// out by the days of f.Zone (Dataset.InZone): every zone shares the build of the filter.
func (s *Service) Dataset(ctx context.Context, f Filter) (Dataset, error) {
	ds, err := s.cachedDataset(ctx, f)
	if err != nil {
		return Dataset{}, err
	}
	return ds.InZone(f.Zone), nil
}

// cachedDataset returns the dataset of the filter with the names of its people filled in, cut from
// the build of its window (HT-527): every person, agent, project and kind of one window shares
// one build. The build is the one ended less than cacheTTL ago; else the one ended less than
// staleMax ago at once, starting one build in the background to replace it; else a new one,
// waiting for it. A *domain.InvalidFilterError when a value of the filter is out of range; the
// context's error when ctx ends first, and the build goes on without it.
func (s *Service) cachedDataset(ctx context.Context, f Filter) (Dataset, error) {
	if err := f.Validate(); err != nil {
		return Dataset{}, err
	}
	key := f.periodKey()
	s.mu.Lock()
	e, ok := s.entries[key]
	building := ok && !isDone(e.done)
	usable := ok && !building && e.err == nil
	age := time.Duration(0)
	if usable {
		age = s.clock.Now().Sub(e.built)
	}
	fresh := usable && age < cacheTTL
	if usable && !fresh && age < staleMax {
		if e.refresh == nil {
			old, r := e, &cacheEntry{done: make(chan struct{})}
			e.refresh = r
			s.refreshes.Go(func() { s.replace(context.WithoutCancel(ctx), f, old, r) })
		}
		s.mu.Unlock()
		return s.view(ctx, e, f)
	}
	switch {
	case usable && !fresh && e.refresh != nil:
		// Past staleMax with a build in the background: wait for it rather than start another.
		r := e.refresh
		s.entries[key] = r
		e = r
		s.mu.Unlock()
	case !building && !fresh:
		s.prune(key)
		e = &cacheEntry{done: make(chan struct{})}
		s.entries[key] = e
		s.mu.Unlock()
		// The build keeps the request's values, not its cancellation.
		go s.build(context.WithoutCancel(ctx), f, e)
	default:
		s.mu.Unlock()
	}
	select {
	case <-e.done:
	case <-ctx.Done():
		return Dataset{}, fmt.Errorf("wait for the dataset: %w", ctx.Err())
	}
	if e.err != nil {
		return Dataset{}, e.err
	}
	return s.view(ctx, e, f)
}

// view is the dataset of f cut from the finished build e, with its people named and its own
// encoding. The first request of a filter starts the cut apart from the requests, so that one
// going away does not stop it; every request of the filter waits for that one cut or for its own
// ctx to end, and the next ones share it.
func (s *Service) view(ctx context.Context, e *cacheEntry, f Filter) (Dataset, error) {
	key := f.cacheKey()
	s.mu.Lock()
	v, ok := e.views[key]
	if !ok {
		if len(e.views) >= maxViews {
			// Only finished cuts go: a request of a filter being cut still joins its cut.
			maps.DeleteFunc(e.views, func(_ string, v *viewEntry) bool { return isDone(v.done) })
		}
		v = &viewEntry{done: make(chan struct{})}
		e.views[key] = v
		go func() {
			defer close(v.done)
			s.cuts.Add(1)
			p, err := e.period.cut(context.WithoutCancel(ctx), s.src, f.Window(e.period.now))
			if err != nil {
				// The failed cut leaves, so that the next request cuts again.
				v.err = err
				s.mu.Lock()
				if e.views[key] == v {
					delete(e.views, key)
				}
				s.mu.Unlock()
				return
			}
			ds := p.dataset(f)
			nameDataset(&ds, e.names)
			ds.Encoded = &Encoded{}
			v.ds = ds
		}()
	}
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Dataset{}, fmt.Errorf("wait for the dataset: %w", err)
	}
	select {
	case <-v.done:
		return v.ds, v.err
	case <-ctx.Done():
		return Dataset{}, fmt.Errorf("wait for the dataset: %w", ctx.Err())
	}
}

// prune drops the finished entries past staleMax and, while the cache holds maxCacheEntries or
// more, or its finished entries hold sessionBudget sessions or more together, the oldest finished
// one, to make room for key. An entry being built stays. Called with s.mu held.
func (s *Service) prune(key string) {
	now := s.clock.Now()
	delete(s.entries, key)
	for k, e := range s.entries {
		if isDone(e.done) && now.Sub(e.built) >= staleMax {
			delete(s.entries, k)
		}
	}
	for len(s.entries) >= maxCacheEntries || s.cachedSessions() >= s.sessionBudget {
		if !s.dropOldest(nil) {
			return
		}
	}
}

// trimSessions drops the oldest finished entries but keep while the finished ones hold more than
// sessionBudget sessions: builds that waited for their slot passed prune while the others held
// nothing yet. Called with s.mu held, when a build ends.
func (s *Service) trimSessions(keep *cacheEntry) {
	for s.cachedSessions() > s.sessionBudget {
		if !s.dropOldest(keep) {
			return
		}
	}
}

// dropOldest drops the oldest finished entry but keep; false when there is none. Called with s.mu
// held.
func (s *Service) dropOldest(keep *cacheEntry) bool {
	oldest := ""
	for k, e := range s.entries {
		if e != keep && isDone(e.done) && (oldest == "" || e.built.Before(s.entries[oldest].built)) {
			oldest = k
		}
	}
	if oldest == "" {
		return false
	}
	delete(s.entries, oldest)
	return true
}

// cachedSessions counts the sessions of the finished entries. Called with s.mu held.
func (s *Service) cachedSessions() int {
	n := 0
	for _, e := range s.entries {
		if isDone(e.done) && e.period != nil {
			n += len(e.period.sessions)
		}
	}
	return n
}

// build builds the dataset into e and closes e.done; a failed build leaves the cache, a finished
// one lets the oldest go past the session budget (trimSessions). It waits for a free slot of
// maxBuilds first, and its buildTimeout counts from then.
func (s *Service) build(ctx context.Context, f Filter, e *cacheEntry) {
	// The end is published under the lock together with the trim, so that no request sees the
	// cache past its budget between the two.
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		close(e.done)
		// A build in the background is not in the cache yet: replace trims once it takes its place.
		if e.err == nil && s.entries[f.periodKey()] == e {
			s.trimSessions(e)
		}
	}()
	// Only the warm-up's builds end with their ctx here: a request's build keeps no cancellation.
	select {
	case s.builds <- struct{}{}:
	case <-ctx.Done():
		s.failed(f, e, fmt.Errorf("wait for a build slot: %w", ctx.Err()))
		return
	}
	defer func() { <-s.builds }()
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	now := s.clock.Now()
	period, err := buildPeriod(ctx, s.src, f.buildFilter().Window(now), now, redact.NewMemo(), sessionWorkers())
	var names map[uuid.UUID]string
	if err == nil {
		names, err = s.names(ctx, period)
	}
	if err != nil {
		s.failed(f, e, err)
		return
	}
	// The age counts from the end of the build: a build longer than cacheTTL would otherwise be
	// stale the moment it ends.
	e.built, e.views = s.clock.Now(), map[string]*viewEntry{}
	e.period, e.names = period, names
	e.sessions = make(map[SessionKey]int, len(period.sessions))
	for i := range period.sessions {
		e.sessions[period.sessions[i].Key] = i
	}
}

// failed ends the build e of f with err: the entry leaves the cache, so that the next request
// builds again. e.done is closed by build.
func (s *Service) failed(f Filter, e *cacheEntry, err error) {
	e.built, e.err, e.views = s.clock.Now(), err, map[string]*viewEntry{}
	s.mu.Lock()
	if s.entries[f.periodKey()] == e {
		delete(s.entries, f.periodKey())
	}
	s.mu.Unlock()
}

// replace builds r in the background and puts it in the place of old while old still holds it;
// once old has left the cache, pruned or replaced, r is dropped, so the cache never grows past
// its bound. A request past staleMax may have put r in that place already to wait for it. A
// failed build leaves old as it is, and the next request after cacheTTL tries again.
func (s *Service) replace(ctx context.Context, f Filter, old, r *cacheEntry) {
	s.build(ctx, f, r)
	key := f.periodKey()
	s.mu.Lock()
	defer s.mu.Unlock()
	old.refresh = nil
	if r.err == nil && s.entries[key] == old {
		s.entries[key] = r
		s.trimSessions(r)
	}
}

// names are the names of the people of the window's sessions.
func (s *Service) names(ctx context.Context, p *periodBuild) (map[uuid.UUID]string, error) {
	var ids []uuid.UUID
	for i := range p.sessions {
		if id := p.sessions[i].Key.UserID; !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[uuid.UUID]string{}, nil
	}
	names, err := s.users.Names(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("name the people of the dataset: %w", err)
	}
	return names, nil
}

// nameDataset fills in the names of the dataset's people.
func nameDataset(ds *Dataset, names map[uuid.UUID]string) {
	ds.UserNames = names
	for i := range ds.Facets.Users {
		ds.Facets.Users[i].Name = names[ds.Facets.Users[i].ID]
	}
	for i := range ds.Sessions {
		ds.Sessions[i].UserName = names[ds.Sessions[i].UserID]
	}
}

// isDone tells whether ch is closed.
func isDone(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// buildFilter is the filter of the build f is cut from: every period that ends now is cut from
// one build of maxDays (HT-536); one that ends at Until has a build of its own.
func (f Filter) buildFilter() Filter {
	if f.Until == nil {
		return Filter{}
	}
	return Filter{Days: f.Days, Until: f.Until}
}

// periodKey tells builds apart: the filters of one key share one build (HT-527, HT-536).
func (f Filter) periodKey() string {
	f = f.buildFilter().withDefaults()
	until := ""
	if f.Until != nil {
		until = f.Until.UTC().Format(time.RFC3339Nano)
	}
	return strconv.Itoa(*f.Days) + "\x00" + until
}

// cacheKey tells filters apart: two filters of one key give one dataset.
func (f Filter) cacheKey() string {
	f = f.withDefaults()
	kinds := make([]string, 0, len(f.Kinds))
	for _, k := range f.Kinds {
		kinds = append(kinds, string(k))
	}
	slices.Sort(kinds)
	kinds = slices.Compact(kinds)
	user, until := "", ""
	if f.UserID != nil {
		user = f.UserID.String()
	}
	if f.Until != nil {
		until = f.Until.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{strconv.Itoa(*f.Days), f.Agent, f.Project, user, strings.Join(kinds, ","), until}, "\x00")
}

// Session returns the timeline of the session sid. agent and userID, when given, tell apart
// sessions that share the id. domain.ErrAnalyticsSessionNotFound when no session of the last
// maxDays matches, domain.ErrAnalyticsSessionAmbiguous when several do. A session named by its
// agent and person that a dataset built less than cacheTTL ago holds answers from the newest
// such dataset (HT-486); any other is built alone.
func (s *Service) Session(ctx context.Context, sid, agent string, userID *uuid.UUID) (SessionTimeline, error) {
	if agent != "" && userID != nil {
		if b, ok := s.cachedSession(SessionKey{UserID: *userID, Agent: agent, SessionID: sid}); ok {
			return b.cachedTimeline(ctx, s.src)
		}
	}
	now := s.clock.Now()
	period := telemetry.Filter{
		SessionID: sid, Agent: agent, From: now.Add(-maxDays*day - historyLookback), To: now.Add(time.Nanosecond),
	}
	if userID != nil {
		period.UserID = *userID
	}
	g, err := streamHooks(ctx, s.src, period, func(*telemetry.HookEvent) {})
	if err != nil {
		return SessionTimeline{}, fmt.Errorf("read the hook events of the session: %w", err)
	}
	var keys []SessionKey
	for k := range g.Sessions {
		if k.SessionID == sid && (agent == "" || k.Agent == agent) && (userID == nil || k.UserID == *userID) {
			keys = append(keys, k)
		}
	}
	switch len(keys) {
	case 0:
		return SessionTimeline{}, domain.ErrAnalyticsSessionNotFound
	case 1:
	default:
		return SessionTimeline{}, domain.ErrAnalyticsSessionAmbiguous
	}
	key := keys[0]
	// The person narrows the native read too: another person's records under the same id stay out.
	native, err := readNative(ctx, s.src, telemetry.Filter{
		SessionID: sid, Agent: key.Agent, UserID: key.UserID, From: period.From, To: period.To,
	})
	if err != nil {
		return SessionTimeline{}, err
	}
	built, err := buildSession(ctx, s.src, key, g.Sessions[key], native, true, redact.NewMemo())
	if err != nil {
		return SessionTimeline{}, err
	}
	return built.Timeline(), nil
}

// cachedSession is the build of the session key in the newest dataset built less than cacheTTL
// ago that holds it; false when none does. Only a session named by its agent and person comes
// from here: the key is then the only one of the id the request can mean, while a dataset holds
// only its window's sessions and so cannot tell an id another person shares. Nor does a session
// come from here that may have begun before the dataset's read (startsInRead).
func (s *Service) cachedSession(key SessionKey) (*SessionBuild, bool) {
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var newest *cacheEntry
	for _, e := range s.entries {
		if !isDone(e.done) || e.err != nil || now.Sub(e.built) >= cacheTTL {
			continue
		}
		if i, ok := e.sessions[key]; ok && startsInRead(&e.period.sessions[i], e.period.win.From.Add(-historyLookback)) &&
			(newest == nil || e.built.After(newest.built)) {
			newest = e
		}
	}
	if newest == nil {
		return nil, false
	}
	return &newest.period.sessions[newest.sessions[key]], true
}

// startsInRead tells whether the session b is known to begin at or after from, where its
// dataset's read begins, so that the dataset holds its whole history: it is not partial, and
// its UUID v7 id was made at or after from, or its first event is a SessionStart of startup or
// clear. A session that has neither may have begun earlier, and is built alone.
func startsInRead(b *SessionBuild, from time.Time) bool {
	if b.Partial.Partial {
		return false
	}
	if !b.Partial.Created.IsZero() {
		return !b.Partial.Created.Before(from)
	}
	first := b.Events[0]
	return first.Event == "SessionStart" && (first.Source == "startup" || first.Source == "clear")
}

// Encoded is the answer a delivery makes of one built dataset: made by the first request it
// answers and shared by the others, with its ETag (HT-489). The zero value is ready; a nil one
// encodes on every call. The dataset laid out in a zone (Dataset.InZone) has an answer of its
// own (In).
type Encoded struct {
	once sync.Once
	body []byte
	etag string
	err  error

	mu    sync.Mutex
	zones map[string]*Encoded
}

// maxEncodedZones bounds the zones whose answers one dataset keeps; a further zone encodes on
// every call, so that tz values cannot pile bodies up in the cache.
const maxEncodedZones = 8

// In is the answer of the dataset laid out in the zone named zone, made once per zone; nil, which
// encodes on every call, for a nil e or past maxEncodedZones zones.
func (e *Encoded) In(zone string) *Encoded {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if z, ok := e.zones[zone]; ok {
		return z
	}
	if len(e.zones) >= maxEncodedZones {
		return nil
	}
	if e.zones == nil {
		e.zones = map[string]*Encoded{}
	}
	z := &Encoded{}
	e.zones[zone] = z
	return z
}

// Of is the body encode makes of the dataset, made on the first call only, and its weak ETag: the
// same body has the same ETag, whatever its content coding on the wire.
func (e *Encoded) Of(encode func() ([]byte, error)) (body []byte, etag string, err error) {
	if e == nil {
		body, err = encode()
		return body, etagOf(body), err
	}
	e.once.Do(func() {
		e.body, e.err = encode()
		e.etag = etagOf(e.body)
	})
	return e.body, e.etag, e.err
}

// etagOf is the weak ETag of body: the first half of its SHA-256.
func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:16]) + `"`
}
