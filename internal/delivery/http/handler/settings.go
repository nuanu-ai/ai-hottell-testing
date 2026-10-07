package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// GetMyTelemetrySettings answers the deny settings of the user of the session in full form
// and their version.
func (a *API) GetMyTelemetrySettings(ctx context.Context, _ openapi.GetMyTelemetrySettingsRequestObject) (
	openapi.GetMyTelemetrySettingsResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	settings, version, err := a.settings.Get(ctx, me)
	if err != nil {
		return nil, err
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal telemetry settings: %w", err)
	}
	return openapi.GetMyTelemetrySettings200JSONResponse{Settings: document, Version: version}, nil
}

// UpdateMyTelemetrySettings saves the deny settings of the user of the session in place of
// the version they were read at and answers them in full form with their new version. A
// document that breaks the schema is answered 422 with where and why.
func (a *API) UpdateMyTelemetrySettings(ctx context.Context, request openapi.UpdateMyTelemetrySettingsRequestObject) (
	openapi.UpdateMyTelemetrySettingsResponseObject, error,
) {
	me, _, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	version, err := a.settings.Update(ctx, me, request.Body.Settings, request.Body.ExpectedVersion)
	if errors.Is(err, domain.ErrInvalidTelemetrySettings) {
		public := domain.ErrInvalidTelemetrySettings
		return openapi.UpdateMyTelemetrySettings422JSONResponse{
			Code: public.Code, Message: public.Message, Detail: invalidSettingsDetail(err),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	// Update accepted the document, so it parses; its full form is what is stored now.
	settings, err := domain.ParseTelemetrySettings(request.Body.Settings)
	if err != nil {
		return nil, fmt.Errorf("parse saved telemetry settings: %w", err)
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal telemetry settings: %w", err)
	}
	return openapi.UpdateMyTelemetrySettings200JSONResponse{Settings: document, Version: version}, nil
}

// invalidSettingsDetail returns where and why a document broke the schema, the text err
// carries after the code of domain.ErrInvalidTelemetrySettings.
func invalidSettingsDetail(err error) string {
	return strings.TrimPrefix(err.Error(), domain.ErrInvalidTelemetrySettings.Code+": ")
}
