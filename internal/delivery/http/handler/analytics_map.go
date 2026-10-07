package handler

import (
	"encoding/json"
	"fmt"

	"git.alva.dev/alva/harness-telemetry/internal/application/analytics"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/http/openapi"
)

// The analytics types carry the JSON names of the contract, so a nested part is translated to
// its openapi type through JSON; the top level is built field by field, so that a part the
// application does not build yet is an empty list, never null.

// viaJSON translates src into dst through its JSON form.
func viaJSON(src, dst any) error {
	b, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("marshal %T: %w", src, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("unmarshal %T into %T: %w", src, dst, err)
	}
	return nil
}

// datasetBody is the dataset in the form of the contract.
func datasetBody(ds analytics.Dataset) (openapi.AnalyticsDataset, error) {
	body := openapi.AnalyticsDataset{
		SchemaVersion: ds.SchemaVersion,
		Variant:       openapi.AnalyticsDatasetVariant(ds.Variant),
		GeneratedAt:   ds.GeneratedAt,
		Window:        openapi.AnalyticsWindow{From: ds.Window.From, To: ds.Window.To},
		Filter:        filterBody(ds.Filter),
		Gaps:          nonNil(ds.Gaps),
		Sessions:      []openapi.AnalyticsSession{},
		Tools:         []openapi.AnalyticsToolRow{},
		Mcp:           []openapi.AnalyticsMcpRow{},
		Commands:      []openapi.AnalyticsCommandRow{},
		Permissions:   []openapi.AnalyticsPermissionRow{},
		Friction:      []openapi.AnalyticsFriction{},
		Skills:        openapi.AnalyticsSkills{Rows: []openapi.AnalyticsSkillRow{}},
		Findings:      []openapi.AnalyticsFinding{},
		Done:          []map[string]any{},
		ChecksCatalog: []openapi.AnalyticsCheckDef{},
	}
	for _, part := range []struct{ src, dst any }{
		{ds.Facets, &body.Facets},
		{ds.Summary, &body.Summary},
		{ds.Pricing, &body.Pricing},
	} {
		if err := viaJSON(part.src, part.dst); err != nil {
			return openapi.AnalyticsDataset{}, err
		}
	}
	for _, part := range []struct {
		n        int
		src, dst any
	}{
		{len(ds.Sessions), ds.Sessions, &body.Sessions},
		{len(ds.Tools), ds.Tools, &body.Tools},
		{len(ds.MCP), ds.MCP, &body.Mcp},
		{len(ds.Commands), ds.Commands, &body.Commands},
		{len(ds.Permissions), ds.Permissions, &body.Permissions},
		{len(ds.Friction), ds.Friction, &body.Friction},
	} {
		if part.n == 0 {
			continue
		}
		if err := viaJSON(part.src, part.dst); err != nil {
			return openapi.AnalyticsDataset{}, err
		}
	}
	if err := viaJSON(ds.Skills, &body.Skills); err != nil {
		return openapi.AnalyticsDataset{}, err
	}
	body.Skills.Rows = nonNil(body.Skills.Rows)
	if len(ds.Findings) > 0 {
		if err := viaJSON(ds.Findings, &body.Findings); err != nil {
			return openapi.AnalyticsDataset{}, err
		}
	}
	return body, nil
}

// filterBody is the filter a dataset was built for, its defaults filled in.
func filterBody(f analytics.Filter) openapi.AnalyticsFilter {
	out := openapi.AnalyticsFilter{Kind: []openapi.AnalyticsSessionKind{}, User: f.UserID}
	if f.Days != nil {
		out.Days = *f.Days
	}
	if f.Agent != "" {
		agent := openapi.AnalyticsAgent(f.Agent)
		out.Agent = &agent
	}
	if f.Project != "" {
		project := f.Project
		out.Project = &project
	}
	for _, k := range f.Kinds {
		out.Kind = append(out.Kind, openapi.AnalyticsSessionKind(k))
	}
	return out
}

// nonNil is s, or an empty list for nil.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
