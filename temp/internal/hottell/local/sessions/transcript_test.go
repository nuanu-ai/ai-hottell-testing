package sessions_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/local/sessions"
)

func kinds(evs []sessions.Event) string {
	ks := make([]string, 0, len(evs))
	for _, e := range evs {
		ks = append(ks, e.Kind)
	}
	return strings.Join(ks, ",")
}

// parse writes body to a temporary file and parses it as a transcript of agent.
func parse(t *testing.T, agent, body string) *sessions.Transcript {
	t.Helper()
	p := writeFile(t, t.TempDir(), "s.jsonl", []byte(body))
	tr, err := sessions.Parse(sessions.Info{Agent: agent, ID: "s", Path: p})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestParseClaude(t *testing.T) {
	t.Parallel()
	tr := parse(t, "claude", claudeFixture)
	want := "attachment,user_message,reasoning,token_usage,tool_call,tool_result,assistant_message,token_usage,system"
	if got := kinds(tr.Events); got != want {
		t.Fatalf("kinds:\n%s\n%s", got, want)
	}
	if tr.Agent != "claude" || tr.ID != "s" || tr.Cwd != "/Users/a/proj.x" || tr.Title != "Башни wifi" ||
		tr.Broken != 1 || tr.Version != "2.1.247" {
		t.Fatalf("meta: %+v", tr)
	}
	// The usage of msg_1 is written twice (once per block) and counted once.
	var tok sessions.TokenUsage
	for _, e := range tr.Events {
		if e.Tokens != nil {
			tok.Add(*e.Tokens)
		}
	}
	if tok.Input != 1102+1203 || tok.CacheRead != 2200 || tok.Output != 70 {
		t.Fatalf("tokens: %+v", tok)
	}
	if e := tr.Events[5]; !e.IsError || e.CallID != "tu_1" || e.Text != "нет такого" {
		t.Fatalf("tool_result: %+v", e)
	}
}

func TestParseCodex(t *testing.T) {
	t.Parallel()
	tr := parse(t, "codex", codexFixture)
	want := "turn_start,system,user_message,tool_call,token_usage,tool_result,reasoning,assistant_message,turn_end"
	if got := kinds(tr.Events); got != want {
		t.Fatalf("kinds:\n%s\n%s", got, want)
	}
	u := tr.Events[4].Tokens
	if u.Input != 38994 || u.CacheRead != 19584 || u.Output != 192 || u.Reasoning != 10 {
		t.Fatalf("usage from token_usage_record, token_count must not double it: %+v", u)
	}
	if tr.Events[5].Text != "a.txt" || tr.Events[7].Model != "gpt-6-astra" || tr.Cwd != "/Users/a/3d" {
		t.Fatalf("events: %+v", tr.Events)
	}
	for i, e := range tr.Events {
		if e.Seq != i {
			t.Fatalf("seq %d != %d", e.Seq, i)
		}
	}

	old := parse(t, "codex", codexOldFixture)
	if len(old.Events) != 1 || old.Events[0].Tokens.Input != 10672 {
		t.Fatalf("old format: %+v", old.Events)
	}
}

func TestParseBrokenCompressed(t *testing.T) {
	t.Parallel()
	p := writeFile(t, t.TempDir(), "rollout-2026-09-20T10-00-00-019e-bad.jsonl.zst", []byte("not zstd"))
	if _, err := sessions.Parse(sessions.Info{Agent: "codex", Path: p, Compressed: true}); err == nil {
		t.Fatal("a rollout that does not unpack is an error, not an empty session")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	if got := sessions.Truncate("abcdef", 3); !strings.HasPrefix(got, "abc…") || !strings.Contains(got, "6") {
		t.Fatalf("cut: %q", got)
	}
	if got := sessions.Truncate("abc", 3); got != "abc" {
		t.Fatalf("short: %q", got)
	}
	if got := sessions.Truncate("abcdef", 0); got != "abcdef" {
		t.Fatalf("no limit: %q", got)
	}
	// "привет" is 12 bytes, two per letter: a cut at 3 falls inside "р" and backs off to "п".
	if got := sessions.Truncate("привет", 3); !utf8.ValidString(got) || !strings.HasPrefix(got, "п…") || !strings.Contains(got, "12") {
		t.Fatalf("cyrillic: %q", got)
	}
	if got := sessions.Truncate("привет", 1); !utf8.ValidString(got) || !strings.HasPrefix(got, "…") {
		t.Fatalf("cyrillic, a cut inside the first letter: %q", got)
	}
	// A four-byte character backs off three bytes to its start.
	if got := sessions.Truncate("😀😀", 7); !strings.HasPrefix(got, "😀…") {
		t.Fatalf("a four-byte character: %q", got)
	}
	// Bytes that are not UTF-8 never back off more than a character could: the cut stays
	// where it was asked instead of going to nothing.
	if got := sessions.Truncate(strings.Repeat("\x80", 10), 5); !strings.HasPrefix(got, strings.Repeat("\x80", 5)+"…") {
		t.Fatalf("invalid UTF-8: %q", got)
	}
}
