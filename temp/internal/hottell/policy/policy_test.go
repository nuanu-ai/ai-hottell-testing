package policy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/policy"
)

const home = "/Users/u"

const examples = "../../../docs/specs/hottell-contract/examples"

// Settings documents the cases run against: the contract examples and variants of them
// for the denials the examples leave out.
func settings(t *testing.T, name string) policy.Settings {
	t.Helper()

	var data []byte
	switch name {
	case "empty", "full":
		var err error
		data, err = os.ReadFile(filepath.Join(examples, "settings-"+name+".json"))
		if err != nil {
			t.Fatal(err)
		}
	case "missing":
		data = []byte(`{}`)
	case "hooks off":
		data = []byte(`{"agents": {"claude": {"sources": {"hooks": false}}},
			"folders": {"denied": ["~/work/**"], "allowed": ["~/work/oss/**"]}}`)
	case "transcripts off":
		data = []byte(`{"agents": {"codex": {"sources": {"transcripts": false}}},
			"folders": {"denied": ["~/work/**"], "allowed": ["~/work/oss/**"]}}`)
	case "patterns":
		data = []byte(`{"folders": {"denied": ["/Volumes/secret/**", "~/src/**/private/", "/opt/exact", "/**/tmp"],
			"allowed": ["/Volumes/secret/open/**", "/elsewhere/**"]}}`)
	case "prompt denied":
		data = []byte(`{"agents": {"claude": {"hook_fields": {"denied": ["prompt"]}}},
			"folders": {"denied": ["~/work/**"], "allowed": ["~/work/oss/**"]}}`)
	case "event denied in allowed folder":
		data = []byte(`{"agents": {"claude": {"hook_events": {"denied": ["UserPromptSubmit"]}}},
			"folders": {"denied": ["~/work/**"], "allowed": ["~/work/oss/**"]}}`)
	default:
		t.Fatalf("unknown settings %q", name)
	}

	s, err := policy.Parse(data, home)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func event(name, cwd string) []byte {
	e := map[string]any{
		"session_id":      "s1",
		"hook_event_name": name,
		"prompt":          "hello",
		"tool_input":      map[string]any{"command": "ls"},
		"tool_response":   "out",
	}
	if cwd != "" {
		e["cwd"] = cwd
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return b
}

func TestHook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
		agent    policy.Agent
		event    []byte
		send     bool
		cut      []string
	}{
		{name: "empty settings send everything", settings: "empty", agent: policy.Claude, event: event("PostToolUse", home+"/work/a"), send: true},
		{name: "missing fields mean allowed", settings: "missing", agent: policy.Codex, event: event("PostToolUse", home+"/work/a"), send: true},

		// One case per kind of denial.
		{name: "agent disabled", settings: "full", agent: policy.Codex, event: event("Stop", "/tmp/x"), send: false},
		{name: "source hooks off", settings: "hooks off", agent: policy.Claude, event: event("Stop", "/tmp/x"), send: false},
		{name: "source hooks off leaves the other agent", settings: "hooks off", agent: policy.Codex, event: event("Stop", "/tmp/x"), send: true},
		{name: "event denied", settings: "full", agent: policy.Claude, event: event("MessageDisplay", "/tmp/x"), send: false},
		{name: "folder denied", settings: "full", agent: policy.Claude, event: event("Stop", home+"/work/client-a"), send: false},
		{name: "folder root of ** denied", settings: "full", agent: policy.Claude, event: event("Stop", home+"/work"), send: false},
		{name: "folder allowed inside denied", settings: "full", agent: policy.Claude, event: event("Stop", home+"/work/oss/tool"), send: true, cut: []string{"tool_response"}},
		{name: "folder outside denials", settings: "full", agent: policy.Claude, event: event("Stop", home+"/projects/x"), send: true, cut: []string{"tool_response"}},
		{name: "field cut", settings: "full", agent: policy.Claude, event: event("PostToolUse", "/tmp/x"), send: true, cut: []string{"tool_response"}},
		{name: "no cwd skips the folder check", settings: "full", agent: policy.Claude, event: event("Stop", ""), send: true, cut: []string{"tool_response"}},

		// Denials that meet.
		{name: "agent disabled beats allowed folder", settings: "full", agent: policy.Codex, event: event("Stop", home+"/work/oss/tool"), send: false},
		{name: "source off beats allowed folder", settings: "hooks off", agent: policy.Claude, event: event("Stop", home+"/work/oss/tool"), send: false},
		{name: "event denied beats allowed folder", settings: "event denied in allowed folder", agent: policy.Claude, event: event("UserPromptSubmit", home+"/work/oss/tool"), send: false},
		{name: "allowed folder still cuts fields", settings: "prompt denied", agent: policy.Claude, event: event("UserPromptSubmit", home+"/work/oss/tool"), send: true, cut: []string{"prompt"}},
		{name: "denied folder stops before fields", settings: "prompt denied", agent: policy.Claude, event: event("UserPromptSubmit", home+"/work/client-a"), send: false},
		{name: "field denial of one agent leaves the other", settings: "prompt denied", agent: policy.Codex, event: event("UserPromptSubmit", home+"/work/oss/tool"), send: true},

		// Malformed input.
		{name: "not a JSON object", settings: "empty", agent: policy.Claude, event: []byte(`[1]`), send: false},
		{name: "not JSON", settings: "empty", agent: policy.Claude, event: []byte(`{`), send: false},
		{name: "unknown agent", settings: "empty", agent: "gemini", event: event("Stop", "/tmp/x"), send: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, send := policy.Hook(settings(t, tt.settings), tt.agent, tt.event)
			if send != tt.send {
				t.Fatalf("send = %v, want %v", send, tt.send)
			}
			if !send {
				return
			}

			var in, got map[string]json.RawMessage
			if err := json.Unmarshal(tt.event, &in); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("out is not JSON: %v", err)
			}
			for _, f := range tt.cut {
				if _, ok := got[f]; ok {
					t.Errorf("field %q not cut", f)
				}
				delete(in, f)
			}
			if len(got) != len(in) {
				t.Errorf("got %d fields, want %d", len(got), len(in))
			}
			for k, v := range in {
				if string(got[k]) != string(v) {
					t.Errorf("field %q = %s, want %s", k, got[k], v)
				}
			}
		})
	}
}

func TestHookReturnsUncutEventAsIs(t *testing.T) {
	t.Parallel()

	in := []byte(`{"z": 1, "session_id": "s", "hook_event_name": "Stop", "cwd": "/tmp/x"}`)
	out, send := policy.Hook(settings(t, "full"), policy.Claude, in)
	if !send || string(out) != string(in) {
		t.Fatalf("Hook = %s, %v; want the event unchanged", out, send)
	}
}

func TestTranscript(t *testing.T) {
	t.Parallel()

	line := []byte(`{"type": "user"}`)
	tests := []struct {
		name     string
		settings string
		agent    policy.Agent
		cwd      string
		want     bool
	}{
		{name: "empty settings send everything", settings: "empty", agent: policy.Codex, cwd: home + "/work/a", want: true},

		{name: "agent disabled", settings: "full", agent: policy.Codex, cwd: "/tmp/x", want: false},
		{name: "source transcripts off", settings: "transcripts off", agent: policy.Codex, cwd: "/tmp/x", want: false},
		{name: "source transcripts off leaves the other agent", settings: "transcripts off", agent: policy.Claude, cwd: "/tmp/x", want: true},
		{name: "source hooks off leaves transcripts", settings: "hooks off", agent: policy.Claude, cwd: "/tmp/x", want: true},
		{name: "folder denied", settings: "full", agent: policy.Claude, cwd: home + "/work/client-a", want: false},
		{name: "folder allowed inside denied", settings: "full", agent: policy.Claude, cwd: home + "/work/oss/tool", want: true},
		{name: "folder outside denials", settings: "full", agent: policy.Claude, cwd: home + "/projects/x", want: true},
		{name: "event and field denials do not touch transcripts", settings: "full", agent: policy.Claude, cwd: "/tmp/x", want: true},

		{name: "agent disabled beats allowed folder", settings: "full", agent: policy.Codex, cwd: home + "/work/oss/tool", want: false},
		{name: "source off beats allowed folder", settings: "transcripts off", agent: policy.Codex, cwd: home + "/work/oss/tool", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.Transcript(settings(t, tt.settings), tt.agent, tt.cwd, line); got != tt.want {
				t.Fatalf("Transcript = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFolderPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cwd    string
		denied bool
	}{
		{cwd: "/Volumes/secret", denied: true},
		{cwd: "/Volumes/secret/", denied: true},
		{cwd: "/Volumes/secret/a/b", denied: true},
		{cwd: "/Volumes/secretive", denied: false},
		{cwd: "/Volumes/Secret/a", denied: false},
		{cwd: "/Volumes/secret/open", denied: false},
		{cwd: "/Volumes/secret/open/x", denied: false},
		{cwd: home + "/src/private", denied: true},
		{cwd: home + "/src/a/b/private/", denied: true},
		{cwd: home + "/src/a/private/sub", denied: false},
		{cwd: "/opt/exact", denied: true},
		{cwd: "/opt/exact/sub", denied: false},
		{cwd: "/opt", denied: false},
		{cwd: "/tmp", denied: true},
		{cwd: "/a/b/tmp", denied: true},
		{cwd: "/a/tmpx", denied: false},
		{cwd: "/elsewhere/tmp", denied: false},
		{cwd: "~/src/private", denied: false},
	}
	s := settings(t, "patterns")
	for _, tt := range tests {
		t.Run(tt.cwd, func(t *testing.T) {
			t.Parallel()

			if got := !policy.Transcript(s, policy.Claude, tt.cwd, nil); got != tt.denied {
				t.Fatalf("denied = %v, want %v", got, tt.denied)
			}
		})
	}
}

func TestHomeExpandsPerMachine(t *testing.T) {
	t.Parallel()

	doc := []byte(`{"folders": {"denied": ["~/work/**"]}}`)
	for _, tt := range []struct {
		home   string
		cwd    string
		denied bool
	}{
		{home: "/Users/a", cwd: "/Users/a/work/x", denied: true},
		{home: "/Users/b/", cwd: "/Users/b/work/x", denied: true},
		{home: "/Users/b", cwd: "/Users/a/work/x", denied: false},
		{home: "", cwd: "/work/x", denied: false},
	} {
		s, err := policy.Parse(doc, tt.home)
		if err != nil {
			t.Fatal(err)
		}
		if got := !policy.Transcript(s, policy.Claude, tt.cwd, nil); got != tt.denied {
			t.Errorf("home %q, cwd %q: denied = %v, want %v", tt.home, tt.cwd, got, tt.denied)
		}
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	if _, err := policy.Parse([]byte(`{"agents": [`), home); err == nil {
		t.Fatal("Parse accepted a broken document")
	}
}
