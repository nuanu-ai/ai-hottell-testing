package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// OTelReport includes only records attributable to this session by exact ID.
// Prompt bodies, tool content and global metrics are never returned.
type OTelReport struct {
	Kind           string         `json:"kind"`
	SessionID      string         `json:"session_id"`
	Coverage       string         `json:"coverage"`
	CoverageDetail string         `json:"coverage_detail"`
	WindowStart    string         `json:"window_start,omitempty"`
	WindowEnd      string         `json:"window_end,omitempty"`
	Queried        bool           `json:"queried"`
	LogGroups      []OTelLogGroup `json:"log_groups"`
	Traces         uint64         `json:"traces"`
	MetricRecords  uint64         `json:"metric_records"`
}

type OTelLogGroup struct {
	Service    string `json:"service"`
	EventName  string `json:"event_name"`
	Events     uint64 `json:"events"`
	FirstEvent string `json:"first_event"`
	LastEvent  string `json:"last_event"`
}

type otelTimeWindow struct {
	From     time.Time
	Until    time.Time
	Coverage string
	Detail   string
	Queried  bool
}

var errOTelWindowUnknown = errors.New("OTel session time range is unknown")

const otelHookBoundsSQL = `SELECT count() AS events,
  toUnixTimestamp64Nano(min(Timestamp)) AS first_ns,
  toUnixTimestamp64Nano(max(Timestamp)) AS last_ns,
  countIf(Body = 'agent.hook.SessionStart') AS starts,
  countIf(Body = 'agent.hook.SessionEnd') AS ends
FROM otel.otel_logs
WHERE Timestamp >= toDateTime64({hook_from:String}, 9, 'UTC')
  AND ServiceName = 'agent-hooks' AND LogAttributes['session_id'] = {sid:String}`

// The source period establishes which partitions may contain session records.
// A one-day guard covers delayed emission and timezone boundary differences.
// The 30-day local retention limits what can be checked for older sessions.
func (s *server) otelWindow(ctx context.Context, id string) (otelTimeWindow, error) {
	day := 24 * time.Hour
	hookFrom := time.Now().UTC().Add(-31 * day).Truncate(day)
	var bounds []struct {
		Events  uint64 `json:"events"`
		FirstNS int64  `json:"first_ns"`
		LastNS  int64  `json:"last_ns"`
		Starts  uint64 `json:"starts"`
		Ends    uint64 `json:"ends"`
	}
	if err := s.query(ctx, otelHookBoundsSQL, url.Values{
		"param_sid": {id}, "param_hook_from": {hookFrom.Format("2006-01-02 15:04:05")},
	}, &bounds); err != nil {
		return otelTimeWindow{}, err
	}
	var first, last time.Time
	partial := false
	if len(bounds) > 0 && bounds[0].Events > 0 {
		first = time.Unix(0, bounds[0].FirstNS).UTC()
		last = time.Unix(0, bounds[0].LastNS).UTC()
		partial = bounds[0].Starts == 0 || bounds[0].Ends == 0
	}
	if retro, err := s.telemetry(id); err == nil {
		start, startErr := time.Parse(time.RFC3339Nano, retro.Period["first_recorded_at"])
		end, endErr := time.Parse(time.RFC3339Nano, retro.Period["last_recorded_at"])
		if startErr == nil && endErr == nil && !end.Before(start) {
			if first.IsZero() || start.Before(first) {
				first = start.UTC()
			}
			if last.IsZero() || end.After(last) {
				last = end.UTC()
			}
		} else if first.IsZero() {
			return otelTimeWindow{}, errOTelWindowUnknown
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return otelTimeWindow{}, err
	}
	if first.IsZero() || last.Before(first) {
		return otelTimeWindow{}, errOTelWindowUnknown
	}
	from := first.Truncate(day).Add(-day)
	until := last.Truncate(day).Add(2 * day)
	retentionThreshold := time.Now().UTC().Add(-30 * day)
	retentionStart := retentionThreshold.Truncate(day)
	if first.Before(retentionThreshold) {
		partial = true
	}
	if from.Before(retentionStart) {
		from = retentionStart
	}
	window := otelTimeWindow{From: from, Until: until, Coverage: "complete", Queried: until.After(from)}
	if partial {
		window.Coverage = "partial"
		window.Detail = "Проверен только доступный период локального OTel; исходная хронология или границы Hooks неполны. Нули не описывают всю сессию."
	} else {
		window.Detail = "Проверен период сессии с запасом в один день, только по точному ID в локальном OTel."
	}
	if !window.Queried {
		window.Coverage = "partial"
		window.Detail = "Период сессии целиком вне 30-дневного срока хранения локального OTel; запросы к событиям не выполнялись."
	}
	return window, nil
}

const otelLogsSQL = `SELECT ServiceName AS service,
  if(LogAttributes['event.name'] = '', 'unknown', LogAttributes['event.name']) AS event_name,
  count() AS events,
  toString(min(Timestamp)) AS first_event,
  toString(max(Timestamp)) AS last_event
FROM otel.otel_logs
WHERE Timestamp >= toDateTime64({from:String}, 9, 'UTC')
  AND Timestamp < toDateTime64({until:String}, 9, 'UTC')
  AND ServiceName != 'agent-hooks'
  AND (LogAttributes['session_id'] = {sid:String} OR LogAttributes['conversation.id'] = {sid:String})
GROUP BY service, event_name ORDER BY events DESC`

const otelTracesSQL = `SELECT count() AS count FROM otel.otel_traces
WHERE Timestamp >= toDateTime64({from:String}, 9, 'UTC')
  AND Timestamp < toDateTime64({until:String}, 9, 'UTC')
  AND (SpanAttributes['session_id'] = {sid:String} OR SpanAttributes['conversation.id'] = {sid:String}
    OR ResourceAttributes['session_id'] = {sid:String} OR ResourceAttributes['conversation.id'] = {sid:String})`

const otelMetricSumSQL = `SELECT count() AS count FROM otel.otel_metrics_sum
WHERE TimeUnix >= toDateTime({from:String}, 'UTC')
  AND TimeUnix < toDateTime({until:String}, 'UTC')
  AND (Attributes['session_id'] = {sid:String} OR Attributes['conversation.id'] = {sid:String}
    OR ResourceAttributes['session_id'] = {sid:String} OR ResourceAttributes['conversation.id'] = {sid:String})`

const otelMetricHistogramSQL = `SELECT count() AS count FROM otel.otel_metrics_histogram
WHERE TimeUnix >= toDateTime({from:String}, 'UTC')
  AND TimeUnix < toDateTime({until:String}, 'UTC')
  AND (Attributes['session_id'] = {sid:String} OR Attributes['conversation.id'] = {sid:String}
    OR ResourceAttributes['session_id'] = {sid:String} OR ResourceAttributes['conversation.id'] = {sid:String})`

func (s *server) otel(ctx context.Context, id string) (OTelReport, error) {
	if !safeID.MatchString(id) {
		return OTelReport{}, os.ErrNotExist
	}
	window, err := s.otelWindow(ctx, id)
	if err != nil {
		return OTelReport{}, err
	}
	report := OTelReport{Kind: "otel", SessionID: id, Coverage: window.Coverage,
		CoverageDetail: window.Detail, Queried: window.Queried}
	if !window.Queried {
		return report, nil
	}
	report.WindowStart = window.From.Format(time.RFC3339)
	report.WindowEnd = window.Until.Format(time.RFC3339)
	params := url.Values{"param_sid": {id},
		"param_from":  {window.From.Format("2006-01-02 15:04:05")},
		"param_until": {window.Until.Format("2006-01-02 15:04:05")}}
	if err := s.query(ctx, otelLogsSQL, params, &report.LogGroups); err != nil && !errors.Is(err, io.EOF) {
		return OTelReport{}, fmt.Errorf("OTel logs: %w", err)
	}
	for _, query := range []struct {
		sql    string
		target *uint64
	}{
		{otelTracesSQL, &report.Traces},
		{otelMetricSumSQL, &report.MetricRecords},
		{otelMetricHistogramSQL, &report.MetricRecords},
	} {
		var rows []struct {
			Count uint64 `json:"count"`
		}
		if err := s.query(ctx, query.sql, params, &rows); err != nil && !errors.Is(err, io.EOF) {
			return OTelReport{}, fmt.Errorf("OTel count: %w", err)
		}
		if len(rows) > 0 {
			*query.target += rows[0].Count
		}
	}
	return report, nil
}

func (r OTelReport) logCount() uint64 {
	var count uint64
	for _, group := range r.LogGroups {
		count += group.Events
	}
	return count
}

func (r OTelReport) hasData() bool { return r.logCount() > 0 || r.Traces > 0 || r.MetricRecords > 0 }

func (s *server) otelAPI(w http.ResponseWriter, r *http.Request) {
	report, err := s.otel(r.Context(), r.PathValue("id"))
	if err != nil {
		serveError(w, err)
		return
	}
	jsonResponse(w, report)
}
