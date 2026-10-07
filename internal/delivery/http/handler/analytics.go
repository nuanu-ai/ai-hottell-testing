package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/application/analytics"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// WithAnalytics gives the API the live analytics; without it the analytics operations answer
// 503 analytics_unavailable.
func (a *API) WithAnalytics(an Analytics) *API {
	a.analytics = an
	return a
}

// analyticsReady checks that a user is signed in and the analytics is wired.
func (a *API) analyticsReady(ctx context.Context) error {
	if _, _, err := signedIn(ctx); err != nil {
		return err
	}
	if a.analytics == nil {
		return domain.ErrAnalyticsUnavailable
	}
	return nil
}

// GetAnalyticsDataset answers the live dataset of the filter. Without kind the dataset keeps the
// sessions of kinds user and automation; user filters the sessions of a person, any signed-in
// user may name any person. A parameter out of range is answered 400 invalid_filter naming it.
func (a *API) GetAnalyticsDataset(
	ctx context.Context, request openapi.GetAnalyticsDatasetRequestObject,
) (openapi.GetAnalyticsDatasetResponseObject, error) {
	if err := a.analyticsReady(ctx); err != nil {
		return nil, err
	}
	p := request.Params
	f := analytics.Filter{Days: p.Days, UserID: p.User}
	var invalid *domain.InvalidFilterError
	if p.Tz != nil {
		zone, err := analytics.ParseZone(*p.Tz)
		if errors.As(err, &invalid) {
			return openapi.GetAnalyticsDataset400JSONResponse{
				Code: domain.ErrInvalidAnalyticsFilter.Code, Message: invalid.Message(),
			}, nil
		}
		f.Zone = zone
	}
	if p.Agent != nil {
		f.Agent = string(*p.Agent)
	}
	if p.Project != nil {
		f.Project = *p.Project
	}
	if p.Kind == nil {
		f.Kinds = analytics.DefaultKinds()
	} else {
		for _, k := range *p.Kind {
			f.Kinds = append(f.Kinds, analytics.SessionKind(k))
		}
	}
	ds, err := a.analytics.Dataset(ctx, f)
	if errors.As(err, &invalid) {
		return openapi.GetAnalyticsDataset400JSONResponse{
			Code: domain.ErrInvalidAnalyticsFilter.Code, Message: invalid.Message(),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	body, etag, err := ds.Encoded.Of(func() ([]byte, error) {
		body, err := datasetBody(ds)
		if err != nil {
			return nil, err
		}
		return json.Marshal(body)
	})
	if err != nil {
		return nil, fmt.Errorf("encode the dataset: %w", err)
	}
	cache := datasetCacheControl
	if p.IfNoneMatch != nil && etagMatches(*p.IfNoneMatch, etag) {
		return openapi.GetAnalyticsDataset304Response{
			Headers: openapi.GetAnalyticsDataset304ResponseHeaders{ETag: &etag, CacheControl: &cache},
		}, nil
	}
	return encodedDataset{body: body, etag: etag}, nil
}

// datasetCacheControl lets the browser keep a dataset and makes it ask again each time, with
// If-None-Match, so that an unchanged dataset comes back as 304 and the browser answers the
// fetch from what it kept (HT-489).
const datasetCacheControl = "private, no-cache"

// encodedDataset is the 200 answer of a dataset already encoded: the body goes out as it is.
type encodedDataset struct {
	body []byte
	etag string
}

func (r encodedDataset) VisitGetAnalyticsDatasetResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", datasetCacheControl)
	w.Header().Set("ETag", r.etag)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(r.body); err != nil {
		return fmt.Errorf("write the dataset: %w", err)
	}
	return nil
}

// etagMatches tells whether an If-None-Match header names etag, by the weak comparison: * or
// one of its listed ETags with or without W/.
func etagMatches(header, etag string) bool {
	opaque := strings.TrimPrefix(etag, "W/")
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == opaque {
			return true
		}
	}
	return false
}

// validAgent checks an agent parameter: claude, codex or none.
func validAgent(agent *openapi.AnalyticsAgent) (string, error) {
	if agent == nil {
		return "", nil
	}
	switch *agent {
	case openapi.Claude, openapi.Codex:
		return string(*agent), nil
	default:
		return "", &domain.InvalidFilterError{Field: "agent", Reason: "claude или codex"}
	}
}

// GetAnalyticsSession answers the timeline of the session id, the agent's session id; agent and
// user tell apart sessions that share it. 404 not_found without such a session, 409
// ambiguous_session when several match, 400 invalid_filter for an unknown agent.
func (a *API) GetAnalyticsSession(
	ctx context.Context, request openapi.GetAnalyticsSessionRequestObject,
) (openapi.GetAnalyticsSessionResponseObject, error) {
	if err := a.analyticsReady(ctx); err != nil {
		return nil, err
	}
	agent, err := validAgent(request.Params.Agent)
	if err != nil {
		return nil, err
	}
	tl, err := a.analytics.Session(ctx, request.Id, agent, request.Params.User)
	if err != nil {
		return nil, err
	}
	var body openapi.AnalyticsTimeline
	if err := viaJSON(tl, &body); err != nil {
		return nil, err
	}
	return openapi.GetAnalyticsSession200JSONResponse(body), nil
}

// GetAnalyticsTeam answers «Команда» for the period: each person's numbers over the sessions
// that agent, project and kind choose. A parameter out of range is answered 400 invalid_filter
// naming it.
func (a *API) GetAnalyticsTeam(
	ctx context.Context, request openapi.GetAnalyticsTeamRequestObject,
) (openapi.GetAnalyticsTeamResponseObject, error) {
	if err := a.analyticsReady(ctx); err != nil {
		return nil, err
	}
	p := request.Params
	f := analytics.TeamFilter{Days: p.Days}
	agent, err := validAgent(p.Agent)
	var invalid *domain.InvalidFilterError
	if errors.As(err, &invalid) {
		return openapi.GetAnalyticsTeam400JSONResponse{
			Code: domain.ErrInvalidAnalyticsFilter.Code, Message: invalid.Message(),
		}, nil
	}
	f.Agent = agent
	if p.Project != nil {
		f.Project = *p.Project
	}
	if p.Kind != nil {
		if !p.Kind.Valid() {
			invalid = &domain.InvalidFilterError{Field: "kind", Reason: "work или all"}
			return openapi.GetAnalyticsTeam400JSONResponse{
				Code: domain.ErrInvalidAnalyticsFilter.Code, Message: invalid.Message(),
			}, nil
		}
		f.AllKinds = *p.Kind == openapi.GetAnalyticsTeamParamsKindAll
	}
	team, err := a.analytics.Team(ctx, f)
	if errors.As(err, &invalid) {
		return openapi.GetAnalyticsTeam400JSONResponse{
			Code: domain.ErrInvalidAnalyticsFilter.Code, Message: invalid.Message(),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	// A pulse that cannot be read leaves pulse null: «Команда» is answered without it (HT-542).
	pulse, pulseErr := a.analytics.TeamPulse(ctx)
	people := make([]openapi.AnalyticsTeamPerson, 0, len(team.People))
	for _, person := range team.People {
		row := openapi.AnalyticsTeamPerson{
			UserId: person.UserID, UserName: person.UserName, Sessions: person.Sessions,
			UserMin: person.UserMin, AgentMin: person.AgentMin, CostUsd: person.CostUSD,
			CostPartial: person.CostPartial, ErrorRate: person.ErrorRate,
			FrictionSessions: person.FrictionSessions, LastActive: person.LastActive,
		}
		if pulseErr == nil {
			row.Pulse = &openapi.AnalyticsTeamPulse{
				From: pulse.From, To: pulse.To, Bucket: openapi.Hour, Values: pulse.Of(person.UserID),
			}
		}
		people = append(people, row)
	}
	return openapi.GetAnalyticsTeam200JSONResponse{
		GeneratedAt: team.GeneratedAt,
		Window:      openapi.AnalyticsWindow{From: team.Window.From, To: team.Window.To},
		People:      people, Projects: team.Projects, HasSystem: team.HasSystem,
		Gaps: team.Gaps, SessionGaps: team.SessionGaps,
	}, nil
}
