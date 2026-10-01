package coach_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/coach"
)

func appliedEntry() m {
	return m{
		"topic_key": "scope-creep-engineering", "agent": "codex", "topic": "Агент раздувает реализацию",
		"findings": []string{"live:abc"},
		"evidence": []m{{"session": "019f-a", "at": "2026-09-30T08:14:02Z", "seq": 412, "quote": "сделай только это, без лишнего"}},
		"decision": "applied", "layer": "instructions", "target": "~/.codex/AGENTS.md",
		"before_sha256": strings.Repeat("a", 64), "after_sha256": strings.Repeat("b", 64),
		"change": "Уточнено правило о минимальной реализации", "rollback": "Вернуть прежнюю строку 12",
		"check": "Задачи на правку кода; сигнал — просьба не раздувать", "check_after": "2026-10-08",
	}
}

func journalAt(t *testing.T) (*coach.Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coach", "journal.jsonl")
	return coach.NewJournal(path), path
}

func TestJournalAppendAndRead(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	if j.Path() != path {
		t.Fatalf("path: %s", j.Path())
	}
	rec, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := rec["id"].(string)
	if at, _ := rec["at"].(string); id == "" || at == "" {
		t.Fatalf("the journal sets id and at: %+v", rec)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the journal's mode: %v %v", st, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 || !json.Valid([]byte(lines[0])) {
		t.Fatalf("one record is one JSON line: %q", b)
	}
	var raw m
	if err := json.Unmarshal([]byte(lines[0]), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"result", "observations"} {
		if v, ok := raw[k]; !ok || v != nil {
			t.Fatalf("a decision has %s: null, as in the schema: %v", k, raw)
		}
	}
	if _, ok := raw["repeats"]; ok {
		t.Fatalf("a decision has no repeats: %v", raw)
	}
	declined := appliedEntry()
	declined["topic_key"], declined["decision"], declined["layer"] = "long-sessions", "declined", nil
	if _, err := j.Append(declined, now); err != nil {
		t.Fatal(err)
	}
	if all, _ := j.Read("", ""); len(all) != 2 {
		t.Fatalf("read: %d", len(all))
	}
	if one, _ := j.Read(id, ""); len(one) != 1 || one[0]["id"] != id {
		t.Fatalf("read id: %+v", one)
	}
	topic, _ := j.Read("", "long-sessions")
	if len(topic) != 1 || topic[0]["decision"] != "declined" {
		t.Fatalf("read topic_key: %+v", topic)
	}
	if v, ok := topic[0]["layer"]; !ok || v != nil {
		t.Fatalf("a declined decision has layer: null: %+v", topic[0])
	}
}

func TestJournalRejects(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	long := appliedEntry()
	long["evidence"] = []m{{"session": "s", "quote": strings.Repeat("я", 201)}}
	noEvidence := appliedEntry()
	delete(noEvidence, "evidence")
	badDecision := appliedEntry()
	badDecision["decision"] = "maybe"
	noCheckAfter := appliedEntry()
	delete(noCheckAfter, "check_after")
	unknown := appliedEntry()
	unknown["checked_at"] = "2026-10-01T00:00:00Z"
	withResult := appliedEntry()
	withResult["result"] = "repeated"
	orphan := m{"check_of": "nope", "topic_key": "x-y", "agent": "codex", "result": "not_enough_data"}
	for name, e := range map[string]m{
		"quote > 200": long, "no quotes": noEvidence, "decision": badDecision,
		"no check_after": noCheckAfter, "unknown field": unknown, "result of a decision": withResult,
		"check without a decision": orphan, "empty": {},
	} {
		if _, err := j.Append(e, now); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("after refusals there is no journal")
	}
	ok := m{
		"id": "x-1", "topic_key": "a-b", "agent": "claude", "topic": "т", "decision": "not_justified", "layer": nil,
		"evidence": []m{{"session": "s", "quote": "q"}},
	}
	if _, err := j.Append(ok, now); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(ok, now); err == nil || !strings.Contains(err.Error(), "x-1") {
		t.Fatalf("a repeated id: %v", err)
	}
}

func TestJournalMasks(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	e := appliedEntry()
	e["evidence"] = []m{{"session": "s1", "quote": "api_key=sk-abcdefghijklmnop в /Users/alex/clients"}}
	e["change"] = "В конфиг добавлен Bearer abcdefghijkl"
	if _, err := j.Append(e, now); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"sk-abcdefghijklmnop", "abcdefghijkl", "/Users/alex"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("%q is left in the journal: %s", leak, b)
		}
	}
}

func TestJournalReadMissing(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	if all, err := j.Read("", ""); err != nil || all == nil || len(all) != 0 {
		t.Fatalf("no file is an empty journal: %v %v", all, err)
	}
}

func TestJournalRecheckAfterNotEnoughData(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	e, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := e["id"].(string)
	check := func(result string, obs any, at time.Time) {
		t.Helper()
		if _, err := j.Append(m{
			"check_of": id, "topic_key": "scope-creep-engineering", "agent": "codex",
			"result": result, "observations": obs,
		}, at); err != nil {
			t.Fatalf("%s: %v", result, err)
		}
	}
	check("not_enough_data", nil, now.Add(7*24*time.Hour))
	got, err := j.Read(id, "")
	if err != nil || len(got) != 1 || got[0]["result"] != "not_enough_data" || got[0]["checks"] != float64(1) {
		t.Fatalf("the first check: %+v %v", got, err)
	}
	check("not_repeated", 4, now.Add(14*24*time.Hour)) // comparable tasks appeared: checked again
	got, err = j.Read(id, "")
	if err != nil || len(got) != 1 || got[0]["result"] != "not_repeated" || got[0]["observations"] != float64(4) ||
		got[0]["checks"] != float64(2) || got[0]["checked_at"] != now.Add(14*24*time.Hour).Format(time.RFC3339) {
		t.Fatalf("the latest check holds: %+v %v", got, err)
	}
}

func TestJournalRepeatsCount(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	e, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := e["id"].(string)
	check := func(extra m) error {
		rec := m{
			"check_of": id, "topic_key": "scope-creep-engineering", "agent": "codex", "result": "repeated",
			"observations": 3, "evidence": []m{{"session": "s2", "quote": "опять раздул"}},
		}
		for k, v := range extra {
			rec[k] = v
		}
		_, err := j.Append(rec, now)
		return err
	}
	if check(nil) == nil || check(m{"repeats": 4}) == nil {
		t.Fatal("repeated without repeats, or with repeats above observations, is refused")
	}
	if err := check(m{"repeats": 1}); err != nil {
		t.Fatal(err)
	}
	got, err := j.Read(id, "")
	if err != nil || len(got) != 1 || got[0]["repeats"] != float64(1) || got[0]["observations"] != float64(3) {
		t.Fatalf("repeated on 1 of 3: %+v %v", got, err)
	}
}

// A hand edit may leave the last line without its newline; the next record must not
// glue onto it.
func TestJournalAppendAfterLineWithoutNewline(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	if _, err := j.Append(appliedEntry(), now); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(string(b), "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	second := appliedEntry()
	second["topic_key"] = "long-sessions"
	if _, err := j.Append(second, now); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 || !json.Valid([]byte(lines[0])) || !json.Valid([]byte(lines[1])) || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("two records, two JSON lines: %q", b)
	}
	if all, err := j.Read("", ""); err != nil || len(all) != 2 {
		t.Fatalf("read: %+v %v", all, err)
	}
}

func TestJournalBrokenLines(t *testing.T) {
	t.Parallel()
	for name, line := range map[string]string{"null": "null", "not JSON": "{oops", "array": "[]"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			j, path := journalAt(t)
			if _, err := j.Append(appliedEntry(), now); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(line + "\n"); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := j.Read("", ""); err == nil || !strings.Contains(err.Error(), "line 2") {
				t.Fatalf("read: want an error at line 2, got %v", err)
			}
			second := appliedEntry()
			second["topic_key"] = "long-sessions"
			if _, err := j.Append(second, now); err == nil {
				t.Fatal("append to a broken journal: want an error")
			}
		})
	}
}

// check is a check record of decision id.
func check(id, result string, extra m) m {
	rec := m{"check_of": id, "topic_key": "scope-creep-engineering", "agent": "codex", "result": result}
	for k, v := range extra {
		rec[k] = v
	}
	return rec
}

func TestJournalCheckHoldsOnlyItsResult(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	e, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := e["id"].(string)
	for field, v := range map[string]any{
		"topic": "т", "findings": []string{"live:abc"}, "target": "~/.codex/AGENTS.md",
		"before_sha256": strings.Repeat("a", 64), "after_sha256": strings.Repeat("b", 64),
		"change": "с", "rollback": "о", "check": "п", "check_after": "2026-10-08",
	} {
		if _, err := j.Append(check(id, "not_enough_data", m{field: v}), now); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("a check with %s: want a refusal naming it, got %v", field, err)
		}
	}
	for _, obs := range []any{nil, 0} {
		if _, err := j.Append(check(id, "not_enough_data", m{"observations": obs}), now); err != nil {
			t.Errorf("not_enough_data with observations %v: %v", obs, err)
		}
	}
	if _, err := j.Append(check(id, "not_enough_data", m{"observations": 2}), now); err == nil {
		t.Error("not_enough_data with 2 observations: want a refusal")
	}
	declined := appliedEntry()
	declined["topic_key"], declined["decision"], declined["layer"], declined["check_after"] = "long-sessions", "declined", nil, "next week"
	if _, err := j.Append(declined, now); err == nil || !strings.Contains(err.Error(), "check_after") {
		t.Errorf("check_after of a declined decision is a date too: %v", err)
	}
	declined["check_after"] = "2026-10-08"
	if _, err := j.Append(declined, now); err != nil {
		t.Errorf("declined with a dated check_after: %v", err)
	}
}

func TestJournalRefsAreClosed(t *testing.T) {
	t.Parallel()
	j, path := journalAt(t)
	for name, change := range map[string]func(e m){
		"id with a space":        func(e m) { e["id"] = "a b" },
		"id of 129":              func(e m) { e["id"] = strings.Repeat("a", 129) },
		"finding with a slash":   func(e m) { e["findings"] = []string{"live:../x"} },
		"empty finding":          func(e m) { e["findings"] = []string{""} },
		"session with a newline": func(e m) { e["evidence"] = []m{{"session": "s\nx", "quote": "q"}} },
		"session as a path":      func(e m) { e["evidence"] = []m{{"session": "/Users/a/x.jsonl", "quote": "q"}} },
		"check_of with a quote":  func(e m) { e["check_of"], e["result"] = `a"b`, "not_enough_data" },
	} {
		e := appliedEntry()
		change(e)
		if _, err := j.Append(e, now); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("after refusals there is no journal")
	}
	e := appliedEntry()
	e["id"], e["findings"] = "20261001T060000-k3f9", []string{"live:abc", "p2:a", "friction:cold.cache_1"}
	if _, err := j.Append(e, now); err != nil {
		t.Fatalf("a generated id and finding ids: %v", err)
	}
	e = appliedEntry()
	e["id"], e["topic_key"] = strings.Repeat("a", 128), "long-sessions"
	if _, err := j.Append(e, now); err != nil {
		t.Fatalf("an id of 128: %v", err)
	}
}

func TestJournalRepeatsGoWithALaterResult(t *testing.T) {
	t.Parallel()
	j, _ := journalAt(t)
	e, err := j.Append(appliedEntry(), now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := e["id"].(string)
	repeated := check(id, "repeated", m{"observations": 3, "repeats": 2, "evidence": []m{{"session": "s2", "quote": "опять"}}})
	for _, later := range []m{check(id, "not_repeated", m{"observations": 4}), check(id, "not_enough_data", nil)} {
		if _, err := j.Append(repeated, now); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Append(later, now); err != nil {
			t.Fatal(err)
		}
		got, err := j.Read(id, "")
		if err != nil || len(got) != 1 {
			t.Fatalf("read: %+v %v", got, err)
		}
		if v, ok := got[0]["repeats"]; ok {
			t.Fatalf("%s after repeated: repeats stays as %v", later["result"], v)
		}
	}
}

// TestJournalAppendConcurrent: concurrent appends of one id, the first of which also
// create coach/, write the record once and refuse the others.
func TestJournalAppendConcurrent(t *testing.T) {
	t.Parallel()
	_, path := journalAt(t)
	const n = 16
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			e := appliedEntry()
			e["id"] = "same-id"
			_, err := coach.NewJournal(path).Append(e, now)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	var ok int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case !strings.Contains(err.Error(), "same-id"):
			t.Errorf("want a refusal of the repeated id, got %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d appends succeeded, want 1", ok)
	}
	if all, err := coach.NewJournal(path).Read("", ""); err != nil || len(all) != 1 {
		t.Fatalf("read: %d %v", len(all), err)
	}
}

func TestJournalNeedsTheDataDir(t *testing.T) {
	t.Parallel()
	data := filepath.Join(t.TempDir(), "missing")
	if _, err := coach.NewJournal(filepath.Join(data, "coach", "journal.jsonl")).Append(appliedEntry(), now); err == nil {
		t.Fatal("a missing data directory: want an error")
	}
	if _, err := os.Stat(data); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the data directory must not be created: %v", err)
	}
	j, path := journalAt(t)
	for range 2 { // the second append finds coach/ in place
		if _, err := j.Append(appliedEntry(), now); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("coach/: %v %v", st, err)
	}
}
