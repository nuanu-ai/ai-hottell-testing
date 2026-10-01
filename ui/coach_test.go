package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoachJournal(t *testing.T) {
	s, dir := testServer(t)
	s.journal = filepath.Join(dir, "coach", "journal.jsonl")
	h := s.routes()
	if rec := get(t, h, "/api/coach/journal"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("нет журнала — пустой список: %d %s", rec.Code, rec.Body)
	}
	writeJournal(t, s.journal, `{"id":"a","topic_key":"k","at":"2026-10-01T06:00:00Z","agent":"codex","decision":"applied","layer":"instructions","change":"x","result":null,"observations":null}
{broken
{"id":"b","check_of":"a","topic_key":"k","at":"2026-10-09T00:00:00Z","agent":"codex","layer":null,"result":"not_repeated","observations":5}
{"id":"b2","check_of":"a","topic_key":"k","at":"2026-10-16T00:00:00Z","agent":"codex","layer":null,"result":"repeated","observations":2,"repeats":1}
{"id":"c","topic_key":"k2","at":"2026-10-02T06:00:00Z","agent":"claude","decision":"declined","layer":null,"result":null,"observations":null}
`)
	rec := get(t, h, "/api/coach/journal")
	var out journalOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.Broken != 1 {
		t.Fatalf("решения без проверок, битая строка посчитана: %+v", out)
	}
	a := out.Entries[0]
	if a["result"] != "repeated" || a["observations"] != float64(2) || a["checked_at"] != "2026-10-16T00:00:00Z" || a["checks"] != float64(2) || a["repeats"] != float64(1) {
		t.Fatalf("в записи a — последняя из двух проверок: %+v", a)
	}
	c := out.Entries[1]
	if c["id"] != "c" {
		t.Fatalf("вторая запись — c: %+v", c)
	}
	for _, k := range []string{"checks", "checked_at"} {
		if _, ok := c[k]; ok {
			t.Fatalf("у c не было проверок, а есть %s: %+v", k, c)
		}
	}
}

// writeJournal creates the journal with its directory; a failed write fails the test.
func writeJournal(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type journalOut struct {
	Entries []map[string]any `json:"entries"`
	Broken  int              `json:"broken_lines"`
}

func journalIDs(t *testing.T, s *server) ([]string, int) {
	t.Helper()
	rec := get(t, s.routes(), "/api/coach/journal")
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	var out journalOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range out.Entries {
		id, _ := e["id"].(string)
		ids = append(ids, id)
	}
	return ids, out.Broken
}

func TestCoachJournalLongLine(t *testing.T) {
	s, dir := testServer(t)
	s.journal = filepath.Join(dir, "journal.jsonl")
	big := `{"id":"big","topic_key":"k","decision":"declined","topic":"` + strings.Repeat("x", 5<<20) + `"}`
	writeJournal(t, s.journal, `{"id":"a","topic_key":"k","decision":"applied"}`+"\n"+big+"\n"+`{"id":"c","topic_key":"k2","decision":"declined"}`+"\n")
	ids, broken := journalIDs(t, s)
	if strings.Join(ids, ",") != "a,big,c" || broken != 0 {
		t.Fatalf("строка больше 4 МиБ не мешает остальным: %v, битых %d", ids, broken)
	}
}

func TestCoachJournalPartialTail(t *testing.T) {
	s, dir := testServer(t)
	s.journal = filepath.Join(dir, "journal.jsonl")
	writeJournal(t, s.journal, `{"id":"a","topic_key":"k","decision":"applied"}`+"\n"+`{broken`+"\n"+`{"id":"d","topic_ke`)
	ids, broken := journalIDs(t, s)
	if strings.Join(ids, ",") != "a" || broken != 1 {
		t.Fatalf("недописанная последняя строка пропускается и не считается битой: %v, битых %d", ids, broken)
	}
}

func TestCoachJournalNullLine(t *testing.T) {
	s, dir := testServer(t)
	s.journal = filepath.Join(dir, "journal.jsonl")
	writeJournal(t, s.journal, `{"id":"a","topic_key":"k","decision":"applied"}`+"\nnull\n"+`{"id":"c","topic_key":"k2","decision":"declined"}`+"\n")
	ids, broken := journalIDs(t, s)
	if strings.Join(ids, ",") != "a,c" || broken != 1 {
		t.Fatalf("строка null — битая, не запись: %v, битых %d", ids, broken)
	}
}

func TestCoachJournalUnreadable(t *testing.T) {
	s, dir := testServer(t)
	s.journal = dir // каталог вместо файла
	if rec := get(t, s.routes(), "/api/coach/journal"); rec.Code != 500 || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("журнал не читается — 500: %d %s", rec.Code, rec.Body)
	}
}
