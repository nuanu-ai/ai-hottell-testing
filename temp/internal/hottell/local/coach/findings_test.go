package coach_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/coach"
)

const (
	sidNew  = "019f0000-0000-7000-8000-00000000000a" // live, inside the period
	sidOld  = "019f0000-0000-7000-8000-00000000000b" // live, before the period
	sidAuto = "019f0000-0000-7000-8000-00000000000c" // a scheduled run
	sidLong = "019f0000-0000-7000-8000-00000000000e" // started in June, continued in September
	sidRev  = "019f0000-0000-7000-8000-00000000000d" // reviewed
)

type m = map[string]any

func ev(sid string, line int, at, text string) m {
	return m{"sid": sid, "line": line, "at": at, "text": text}
}

// dashboardData is the dashboard's data directory: both datasets and the proposal registry.
func dashboardData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	put := func(rel string, v any) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("ui-live/dataset.json", m{
		"variant": "live", "generated_at": "2026-10-01T06:00:00Z",
		"window": m{"from": "2026-06-01T00:00:00Z", "to": "2026-10-01T06:00:00Z"},
		"sessions": []m{
			{"id": sidNew, "kind": "user", "start": "2026-09-30T08:00:00Z", "end": "2026-09-30T09:00:00Z"},
			{"id": sidOld, "kind": "user", "start": "2026-09-21T08:00:00Z", "end": "2026-09-21T09:00:00Z"},
			{"id": sidAuto, "kind": "automation", "start": "2026-09-30T10:00:00Z", "end": "2026-09-30T17:00:00Z"},
			{"id": sidLong, "kind": "user", "start": "2026-06-10T08:00:00Z", "end": "2026-09-30T12:00:00Z"},
		},
		"findings": []m{
			{
				"id": "live:new", "title": "Разобрать эпизоды «повтор вызова»", "kind": "habit", "pattern_id": "retry", "sev": "warn",
				"readiness": "hypothesis", "sessions": []string{sidNew},
				"ev": []m{
					ev(sidNew, 10, "2026-09-30T08:10:00Z", "1/4"), ev(sidNew, 11, "2026-09-30T08:11:00Z", "2/4"),
					ev(sidNew, 12, "2026-09-30T08:12:00Z", "3/4"), ev(sidNew, 13, "2026-09-30T08:13:00Z", "4/4"),
				},
			},
			{
				"id": "live:old", "title": "Старое", "kind": "diagnostic", "sessions": []string{sidOld},
				"ev": []m{ev(sidOld, 3, "2026-09-21T08:05:00Z", "старое")},
			},
			// review remark 3: the session meets the window, but its only evidence is from June
			{
				"id": "live:june", "title": "Июньское", "kind": "habit", "sessions": []string{sidLong},
				"ev": []m{ev(sidLong, 5, "2026-06-10T08:30:00Z", "июнь")},
			},
			{
				"id": "live:health", "title": "Нет PostToolUse", "kind": "diagnostic", "scope": "collection",
				"sessions": []string{sidNew}, "ev": []m{},
			},
		},
		"friction": []m{{
			"key": "coldcache", "name": "Холодный кэш", "sev": "warn", "sessions": []string{sidNew},
			"evidence": []m{ev(sidNew, 20, "2026-09-30T08:30:00Z", "пауза 12 мин")},
		}},
		"skills": m{"rows": []m{
			{
				"name": "playwright", "state": "used", "activations": 2, "sessions": []string{sidNew},
				"evidence": []m{ev(sidNew, 30, "2026-09-30T08:40:00Z", "skill")},
			},
			{
				"name": "pdf", "state": "used", "activations": 1, "sessions": []string{sidLong},
				"evidence": []m{ev(sidLong, 7, "2026-06-10T09:00:00Z", "skill")},
			},
		}},
	})
	put("ui/dataset.json", m{
		"generated_at": "2026-09-30T15:00:00Z",
		"sessions":     []m{{"id": sidRev, "start": "2026-09-29T10:00:00Z", "end": "2026-09-30T11:00:00Z"}},
		"findings": []m{{
			"id": "p2:a", "title": "План назван готовым до ревью", "kind": "skill", "pattern_id": "D05", "sev": "bad",
			"readiness": "needs_spec", "decision": "not_requested", "execution": "not_applied", "scope": "Сверка плана",
			"sessions": []string{sidRev}, "ev": []m{ev(sidRev, 1157, "2026-09-30T10:35:41.515Z", "план готов")},
		}},
	})
	put("reports/v2/proposals.json", m{"kind": "proposal_registry", "schema_version": 2, "proposals": []m{
		{
			"proposal_id": "p2:a", "pattern_id": "D05", "change_type": "skill", "action": "Сверить план",
			"decision": m{"status": "approved"}, "execution": m{"status": "not_applied"}, "readiness": m{"status": "needs_specification"},
			"sources": []m{{"session_id": sidRev, "evidence": []string{"L1157"}}},
		},
		{
			"proposal_id": "p2:b", "pattern_id": "D12", "change_type": "workflow", "action": "Записывать критерий готовности",
			"decision": m{"status": "not_requested"}, "execution": m{"status": "not_applied"}, "readiness": m{"status": "hypothesis"},
			"sources": []m{{"session_id": sidRev, "evidence": []string{"L42", "L43"}}},
		},
	}})
	return dir
}

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed clock of the tests

func byID(fs []coach.FindingBrief) map[string]coach.FindingBrief {
	out := map[string]coach.FindingBrief{}
	for _, f := range fs {
		out[f.ID] = f
	}
	return out
}

func TestFindingsNeedGroundsInsideTheWindow(t *testing.T) {
	t.Parallel()
	out, err := coach.Findings(dashboardData(t), coach.FindingsIn{Since: "2026-09-29T00:00:00Z", Until: "2026-10-02T00:00:00Z"}, now)
	if err != nil {
		t.Fatal(err)
	}
	in, undated := byID(out.Findings), byID(out.Undated)
	for _, id := range []string{"live:old", "live:june"} {
		if _, ok := in[id]; ok {
			t.Errorf("%s: no evidence inside the window, yet the finding is in the period", id)
		}
		if _, ok := undated[id]; ok {
			t.Errorf("%s: the time of its evidence is known, so it is not undated", id)
		}
	}
	nw := in["live:new"]
	if nw.Source != "live" || len(nw.Evidence) != 3 || nw.EvidenceTotal != 4 || nw.Evidence[0].LineOf != "live_timeline" {
		t.Fatalf("live:new: %+v", nw)
	}
	// The builders keep evidence oldest to newest; the finding keeps the newest, in order.
	var texts []string
	for _, e := range nw.Evidence {
		texts = append(texts, e.Text)
	}
	if want := []string{"2/4", "3/4", "4/4"}; !slices.Equal(texts, want) {
		t.Fatalf("live:new evidence %q, want the newest %q", texts, want)
	}
	if fr := in["friction:coldcache"]; fr.Kind != "friction" || len(fr.Evidence) != 1 {
		t.Fatalf("friction: %+v", fr)
	}
	a := in["p2:a"]
	if a.Source != "reviewed" || a.Decision != "approved" || len(a.Evidence) == 0 ||
		a.Evidence[0].LineOf != "rollout" || a.Evidence[0].Line != 1157 {
		t.Fatalf("p2:a: the statuses come from the registry: %+v", a)
	}
	b := undated["p2:b"] // only in the registry, its evidence has no time
	if b.Title != "Записывать критерий готовности" || len(b.Evidence) != 2 || b.Evidence[0].Line != 42 || len(b.Sessions) != 1 {
		t.Fatalf("p2:b: %+v", b)
	}
	if h, ok := undated["live:health"]; !ok || !h.Collection {
		t.Fatalf("a finding without evidence is undated and marked collection: %+v", h)
	}
	if len(out.Skills) != 1 || out.Skills[0].Name != "playwright" || out.Skills[0].EvidenceInPeriod != 1 {
		t.Fatalf("skills follow the evidence of the window: %+v", out.Skills)
	}
	if len(out.ServiceSessions) != 1 || out.ServiceSessions[0].ID != sidAuto {
		t.Fatalf("service: %+v", out.ServiceSessions)
	}
	for _, s := range out.Sources {
		if s.State != "ok" {
			t.Fatalf("%s: %s %s", s.Name, s.State, s.Error)
		}
	}
}

func TestFindingsWithoutWindowByIDAndSource(t *testing.T) {
	t.Parallel()
	dir := dashboardData(t)
	all, err := coach.Findings(dir, coach.FindingsIn{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byID(all.Findings)["live:june"]; !ok || len(all.Undated) != 0 {
		t.Fatalf("without a window: every finding, no undated: %d %d", len(all.Findings), len(all.Undated))
	}
	one, err := coach.Findings(dir, coach.FindingsIn{ID: "friction:coldcache"}, now)
	if err != nil || len(one.Findings) != 1 || one.Findings[0].ID != "friction:coldcache" {
		t.Fatalf("by id: %+v %v", one.Findings, err)
	}
	live, err := coach.Findings(dir, coach.FindingsIn{Source: "live"}, now)
	if err != nil || len(live.Sources) != 1 || live.Sources[0].Name != "live" {
		t.Fatalf("source=live: %+v %v", live.Sources, err)
	}
	if _, err := coach.Findings(dir, coach.FindingsIn{Source: "both"}, now); err == nil {
		t.Fatal("an unknown source is an error")
	}
}

func TestFindingsMissingFiles(t *testing.T) {
	t.Parallel()
	out, err := coach.Findings(t.TempDir(), coach.FindingsIn{}, now)
	if err != nil || len(out.Findings) != 0 || len(out.Sources) != 3 {
		t.Fatalf("without files: %+v %v", out, err)
	}
	for _, s := range out.Sources {
		if s.State != "missing" {
			t.Fatalf("%s: %s", s.Name, s.State)
		}
	}
}

// writeRaw replaces a file of the data directory with body as it is.
func writeRaw(t *testing.T, dir, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindingsRegistryStatusOnlyWhenSet(t *testing.T) {
	t.Parallel()
	dir := dashboardData(t)
	writeRaw(t, dir, "reports/v2/proposals.json",
		`{"proposals":[{"proposal_id":"p2:a","decision":{"status":""},"execution":{"status":"applied"}}]}`)
	out, err := coach.Findings(dir, coach.FindingsIn{Source: "reviewed"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if a := byID(out.Findings)["p2:a"]; a.Decision != "not_requested" || a.Execution != "applied" {
		t.Fatalf("an empty registry status keeps the dataset's, a set one replaces it: %+v", a)
	}
}

// TestFindingsBrokenSourceGivesNothing: JSON fills the fields that come before a type
// error; a broken source must not pass those on.
func TestFindingsBrokenSourceGivesNothing(t *testing.T) {
	t.Parallel()
	dir := dashboardData(t)
	writeRaw(t, dir, "ui-live/dataset.json", `{
		"findings":[{"id":"live:x","title":"x","sessions":["s"],"ev":[{"sid":"s","at":"2026-09-30T08:00:00Z"}]}],
		"friction":[{"key":"k","name":"k","sessions":["s"],"evidence":[{"sid":"s","at":"2026-09-30T08:00:00Z"}]}],
		"skills":{"rows":[{"name":"pdf","activations":1}]},
		"sessions":[{"id":"s","kind":"system","start":"2026-09-30T08:00:00Z","end":"2026-09-30T09:00:00Z"}],
		"generated_at":5}`)
	writeRaw(t, dir, "reports/v2/proposals.json", `{"proposals":[
		{"proposal_id":"p2:a","decision":{"status":"approved"}},
		{"proposal_id":"p2:c","action":"c","sources":[{"session_id":"s","evidence":["L1"]}]},
		{"proposal_id":7}]}`)
	out, err := coach.Findings(dir, coach.FindingsIn{}, now)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, s := range out.Sources {
		states[s.Name] = s.State
	}
	if states["live"] != "broken" || states["reviewed"] != "ok" || states["registry"] != "broken" {
		t.Fatalf("states: %v", states)
	}
	if len(out.Findings) != 1 || len(out.Undated) != 0 || len(out.Skills) != 0 || len(out.ServiceSessions) != 0 {
		t.Fatalf("only the reviewed dataset counts: %+v", out)
	}
	if a := out.Findings[0]; a.ID != "p2:a" || a.Decision != "not_requested" {
		t.Fatalf("a broken registry overrides nothing: %+v", a)
	}
}
