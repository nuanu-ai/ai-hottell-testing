package handler

import (
	"context"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

// analyticsUser checks the analytics like analyticsReady and returns the signed-in user.
func (a *API) analyticsUser(ctx context.Context) (uuid.UUID, error) {
	if err := a.analyticsReady(ctx); err != nil {
		return uuid.Nil, err
	}
	me, _, err := signedIn(ctx)
	return me, err
}

// GetAnalyticsPulse answers the hook events of the last ten minutes and the running sessions, of
// the person and agent when given. A failed read is a pulse with error and empty bars and active.
func (a *API) GetAnalyticsPulse(
	ctx context.Context, request openapi.GetAnalyticsPulseRequestObject,
) (openapi.GetAnalyticsPulseResponseObject, error) {
	if err := a.analyticsReady(ctx); err != nil {
		return nil, err
	}
	agent, err := validAgent(request.Params.Agent)
	if err != nil {
		return nil, err
	}
	pulse, err := a.analytics.Pulse(request.Params.User, agent)
	if err != nil {
		return nil, err
	}
	var body openapi.AnalyticsPulse
	if err := viaJSON(pulse, &body); err != nil {
		return nil, err
	}
	return openapi.GetAnalyticsPulse200JSONResponse(body), nil
}

// GetAnalyticsDelivery answers how sending works for user when given, else for the signed-in
// user.
func (a *API) GetAnalyticsDelivery(
	ctx context.Context, request openapi.GetAnalyticsDeliveryRequestObject,
) (openapi.GetAnalyticsDeliveryResponseObject, error) {
	me, err := a.analyticsUser(ctx)
	if err != nil {
		return nil, err
	}
	if request.Params.User != nil {
		me = *request.Params.User
	}
	d, err := a.analytics.Delivery(ctx, me)
	if err != nil {
		return nil, err
	}
	var body openapi.AnalyticsDelivery
	if err := viaJSON(d, &body); err != nil {
		return nil, err
	}
	return openapi.GetAnalyticsDelivery200JSONResponse(body), nil
}

// ListHiddenTopics answers the topics the signed-in user marked «not a problem».
func (a *API) ListHiddenTopics(
	ctx context.Context, _ openapi.ListHiddenTopicsRequestObject,
) (openapi.ListHiddenTopicsResponseObject, error) {
	me, err := a.analyticsUser(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := a.analytics.HiddenTopics(ctx, me)
	if err != nil {
		return nil, err
	}
	return openapi.ListHiddenTopics200JSONResponse{Keys: nonNil(keys)}, nil
}

// ClearHiddenTopics shows every topic the signed-in user hid again.
func (a *API) ClearHiddenTopics(
	ctx context.Context, _ openapi.ClearHiddenTopicsRequestObject,
) (openapi.ClearHiddenTopicsResponseObject, error) {
	me, err := a.analyticsUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.analytics.UnhideAllTopics(ctx, me); err != nil {
		return nil, err
	}
	return openapi.ClearHiddenTopics204Response{}, nil
}

// HideTopic hides the topic for the signed-in user; hiding it again is no error.
func (a *API) HideTopic(
	ctx context.Context, request openapi.HideTopicRequestObject,
) (openapi.HideTopicResponseObject, error) {
	me, err := a.analyticsUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.analytics.HideTopic(ctx, me, request.Key); err != nil {
		return nil, err
	}
	return openapi.HideTopic204Response{}, nil
}

// UnhideTopic shows the topic to the signed-in user again; a topic not hidden is no error.
func (a *API) UnhideTopic(
	ctx context.Context, request openapi.UnhideTopicRequestObject,
) (openapi.UnhideTopicResponseObject, error) {
	me, err := a.analyticsUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.analytics.UnhideTopic(ctx, me, request.Key); err != nil {
		return nil, err
	}
	return openapi.UnhideTopic204Response{}, nil
}
