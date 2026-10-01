package mcp_test

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp"
	"git.alva.dev/alva/harness-telemetry/internal/delivery/mcp/mock"
)

// installOrigin is a synthetic HT_PUBLIC_ORIGIN other than origin, so that the command is
// seen to come from the configured address.
const installOrigin = "https://telemetry.example.test"

// newInstallServer serves the MCP handler with the keys of alice and bob on installOrigin.
func newInstallServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctrl := gomock.NewController(t)
	srv := httptest.NewServer(mcp.New(knownKeys(t, ctrl), knownUsers(ctrl), mock.NewMockSettings(ctrl),
		installOrigin, version, slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)
	return srv
}

func TestInstallInstructionsCommandUsesPublicOrigin(t *testing.T) {
	t.Parallel()
	session, err := connect(t, newInstallServer(t), "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "get_install_instructions", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call get_install_instructions: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_install_instructions failed: %+v", res.Content)
	}
	var out struct {
		Command     string   `json:"command"`
		Summary     string   `json:"summary"`
		ManualSteps []string `json:"manual_steps"`
	}
	structured := mustJSON(t, res.StructuredContent)
	if err := json.Unmarshal([]byte(structured), &out); err != nil {
		t.Fatalf("decode structured content %s: %v", structured, err)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok || len(res.Content) != 1 {
		t.Fatalf("content: got %+v, want one text", res.Content)
	}
	if got := mustJSON(t, json.RawMessage(text.Text)); got != structured {
		t.Fatalf("text content: got %s, want %s", got, structured)
	}

	if want := "curl -fsSL " + installOrigin + "/install.sh | sh"; out.Command != want {
		t.Fatalf("command: got %q, want %q", out.Command, want)
	}
	if strings.Contains(structured, keyAlice) {
		t.Fatalf("result carries the MCP key: %s", structured)
	}
	if out.Summary == "" {
		t.Fatalf("summary is empty")
	}
	if len(out.ManualSteps) != 3 || !strings.Contains(out.ManualSteps[1], "Перезапустите") ||
		!strings.Contains(out.ManualSteps[2], "/hooks") {
		t.Fatalf("manual_steps: got %q, want the foreign hooks, the restart of the sessions and /hooks for Codex", out.ManualSteps)
	}
	// The flag that removes foreign hooks goes through install.sh, and only with the user's consent.
	foreign := out.ManualSteps[0]
	for _, want := range []string{out.Command + " -s -- --replace-foreign-hooks", "покажите пользователю", "Только с его согласия"} {
		if !strings.Contains(foreign, want) {
			t.Fatalf("manual_steps[0]: got %q, want it to contain %q", foreign, want)
		}
	}
}

func TestInstallInstructionsSchemas(t *testing.T) {
	t.Parallel()
	session, err := connect(t, newInstallServer(t), "Bearer "+keyAlice)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var tool *sdkmcp.Tool
	for _, candidate := range tools.Tools {
		if candidate.Name == "get_install_instructions" {
			tool = candidate
		}
	}
	if tool == nil {
		t.Fatalf("tools: got %+v, want get_install_instructions", tools.Tools)
	}
	if a := tool.Annotations; a == nil || !a.ReadOnlyHint {
		t.Fatalf("annotations: got %+v, want readOnlyHint", a)
	}
	if !strings.Contains(tool.Description, "после подтверждения пользователя") {
		t.Fatalf("description: got %q, want it to allow running the command after the user confirms", tool.Description)
	}
	if got, want := mustJSON(t, tool.InputSchema), `{"additionalProperties":false,"type":"object"}`; got != want {
		t.Fatalf("input schema: got %s, want %s", got, want)
	}
	var output struct {
		Type                 string   `json:"type"`
		Required             []string `json:"required"`
		AdditionalProperties any      `json:"additionalProperties"`
		Properties           map[string]struct {
			Type  string `json:"type"`
			Items struct {
				Type string `json:"type"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(mustJSON(t, tool.OutputSchema)), &output); err != nil {
		t.Fatalf("decode output schema: %v", err)
	}
	if output.Type != "object" || strings.Join(output.Required, ",") != "command,summary,manual_steps" ||
		output.AdditionalProperties != false || len(output.Properties) != 3 ||
		output.Properties["command"].Type != "string" || output.Properties["summary"].Type != "string" ||
		output.Properties["manual_steps"].Type != "array" || output.Properties["manual_steps"].Items.Type != "string" {
		t.Fatalf("output schema: got %s", mustJSON(t, tool.OutputSchema))
	}

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "get_install_instructions", Arguments: map[string]any{"extra": 1},
	})
	if err != nil {
		t.Fatalf("call with extra argument: %v", err)
	}
	if !res.IsError {
		t.Fatalf("call with extra argument: got %+v, want isError", res)
	}
}
