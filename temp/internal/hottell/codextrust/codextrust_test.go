package codextrust_test

import (
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/codextrust"
)

func ptr[T any](v T) *T { return &v }

// The want hashes were accepted by codex-cli 0.159.0 in a live run: with them as
// trusted_hash every one of these hooks fired from `codex exec`.
func TestHashMatchesCodex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   string
		matcher *string
		handler codextrust.Handler
		want    string
	}{
		{
			name:    "defaults",
			event:   codextrust.UserPromptSubmit,
			handler: codextrust.Handler{Command: `echo user_prompt_submit >> "$HOTTELL_MARKER"`},
			want:    "sha256:6d422c8280387b8b127e3e34a709a3110a58e24da3d4eaa771838f8914022330",
		},
		{
			name:    "matcher, timeout and a status message with HTML characters",
			event:   codextrust.SessionStart,
			matcher: ptr("startup|resume"),
			handler: codextrust.Handler{
				Command:       `echo session_start >> "$HOTTELL_MARKER"`,
				TimeoutSec:    ptr[uint64](30),
				StatusMessage: ptr(`Recording <hottell> & "co"`),
			},
			want: "sha256:fb3511c744e98b046b401b42b2991dab58f33a7c14ca998c345879391f3600de",
		},
		{
			name:    "async, matcher and context limit dropped for Stop",
			event:   codextrust.Stop,
			matcher: ptr("ignored"),
			handler: codextrust.Handler{
				Command:                `echo stop >> "$HOTTELL_MARKER"`,
				Async:                  true,
				AdditionalContextLimit: ptr[uint64](100),
			},
			want: "sha256:8d0409ad0cd50d115d6a85e0254c76ac0b53e41fea3af9ccd168d28ed9f7c882",
		},
		{
			name:    "SessionEnd timeout clamped to three seconds",
			event:   codextrust.SessionEnd,
			handler: codextrust.Handler{Command: `echo session_end >> "$HOTTELL_MARKER"`, TimeoutSec: ptr[uint64](9)},
			want:    "sha256:656340b0b206add20424ff758e8b5b7312531a7283d352a22c01efc5a1827a39",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := codextrust.Hash(tt.event, tt.matcher, tt.handler)
			if err != nil {
				t.Fatalf("Hash() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Hash() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestHashNormalizesDefaults(t *testing.T) {
	t.Parallel()

	command := `echo x >> "$HOTTELL_MARKER"`
	tests := []struct {
		name     string
		event    string
		implicit codextrust.Handler
		explicit codextrust.Handler
	}{
		{
			name:     "default timeout",
			event:    codextrust.PreToolUse,
			implicit: codextrust.Handler{Command: command},
			explicit: codextrust.Handler{Command: command, TimeoutSec: ptr[uint64](600)},
		},
		{
			name:     "SessionEnd default timeout",
			event:    codextrust.SessionEnd,
			implicit: codextrust.Handler{Command: command},
			explicit: codextrust.Handler{Command: command, TimeoutSec: ptr[uint64](1)},
		},
		{
			name:     "default context limit",
			event:    codextrust.PostToolUse,
			implicit: codextrust.Handler{Command: command},
			explicit: codextrust.Handler{Command: command, AdditionalContextLimit: ptr[uint64](2500)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			implicit, err := codextrust.Hash(tt.event, nil, tt.implicit)
			if err != nil {
				t.Fatalf("Hash(implicit) error = %v", err)
			}
			explicit, err := codextrust.Hash(tt.event, nil, tt.explicit)
			if err != nil {
				t.Fatalf("Hash(explicit) error = %v", err)
			}
			if implicit != explicit {
				t.Fatalf("Hash(implicit) = %s, Hash(explicit) = %s, want equal", implicit, explicit)
			}
		})
	}
}

func TestHashRejects(t *testing.T) {
	t.Parallel()

	if _, err := codextrust.Hash("Notification", nil, codextrust.Handler{Command: "true"}); err == nil {
		t.Fatal("Hash(unknown event) error = nil, want an error")
	}
	if _, err := codextrust.Hash(codextrust.Stop, nil, codextrust.Handler{Command: "  "}); err == nil {
		t.Fatal("Hash(blank command) error = nil, want an error")
	}
}

func TestKey(t *testing.T) {
	t.Parallel()

	got, err := codextrust.Key("/Users/u/.codex/hooks.json", codextrust.PermissionRequest, 2, 1)
	if err != nil {
		t.Fatalf("Key() error = %v", err)
	}
	if want := "/Users/u/.codex/hooks.json:permission_request:2:1"; got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if _, err := codextrust.Key("/x/hooks.json", "Notification", 0, 0); err == nil {
		t.Fatal("Key(unknown event) error = nil, want an error")
	}
}
