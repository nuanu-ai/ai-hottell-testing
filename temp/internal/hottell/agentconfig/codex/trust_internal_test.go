package codex

import (
	"encoding/json"
	"testing"
)

// The hooks of the live run of HT-77, as hooks.json holds them: codex-cli 0.159.0 ran
// each of them with the want hash as trusted_hash, so reading the handler fields from the
// file has to give the same hashes.
func TestHandlerTrustMatchesCodex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		event string
		group string
		want  string
	}{
		{
			event: "UserPromptSubmit",
			group: `{"hooks":[{"type":"command","command":"echo user_prompt_submit >> \"$HOTTELL_MARKER\""}]}`,
			want:  "sha256:6d422c8280387b8b127e3e34a709a3110a58e24da3d4eaa771838f8914022330",
		},
		{
			event: "SessionStart",
			group: `{"matcher":"startup|resume","hooks":[{"type":"command","command":"echo session_start >> \"$HOTTELL_MARKER\"","timeout":30,"statusMessage":"Recording <hottell> & \"co\""}]}`,
			want:  "sha256:fb3511c744e98b046b401b42b2991dab58f33a7c14ca998c345879391f3600de",
		},
		{
			event: "Stop",
			group: `{"matcher":"ignored","hooks":[{"type":"command","command":"echo stop >> \"$HOTTELL_MARKER\"","async":true,"additionalContextLimit":100}]}`,
			want:  "sha256:8d0409ad0cd50d115d6a85e0254c76ac0b53e41fea3af9ccd168d28ed9f7c882",
		},
		{
			event: "SessionEnd",
			group: `{"hooks":[{"type":"command","command":"echo session_end >> \"$HOTTELL_MARKER\"","timeout":9}]}`,
			want:  "sha256:656340b0b206add20424ff758e8b5b7312531a7283d352a22c01efc5a1827a39",
		},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			t.Parallel()

			var g struct {
				Hooks []json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(tt.group), &g); err != nil {
				t.Fatal(err)
			}
			got, err := handlerTrust(tt.event, "/codex/hooks.json", 0, 0, json.RawMessage(tt.group), g.Hooks[0])
			if err != nil {
				t.Fatalf("handlerTrust() error = %v", err)
			}
			if got.hash != tt.want {
				t.Errorf("hash = %s, want %s", got.hash, tt.want)
			}
		})
	}
}
