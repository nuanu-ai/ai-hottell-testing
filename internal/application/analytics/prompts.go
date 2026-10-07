package analytics

import (
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// promptRawMax is the most characters of a prompt's raw text kept for the session's kind.
const promptRawMax = 300

// Prompt is one UserPromptSubmit of a session: what the person wrote, as PromptText reads it.
type Prompt struct {
	At time.Time
	// Text and Kind are PromptText of the raw prompt.
	Text, Kind string
	// TurnID and PromptID are the turn of Codex and the prompt of Claude the prompt opens.
	TurnID, PromptID string
	// Title is the session title the client sent with the prompt.
	Title string
	// Raw is the start of the prompt as sent, at most promptRawMax characters.
	Raw string
}

// Person tells whether the prompt is the person's: a typed prompt or a reply, not the agent's
// notice (PromptKindSystem).
func (p Prompt) Person() bool { return p.Kind != PromptKindSystem }

// PersonPrompts are the prompts of the person, the agent's notices left out, in their order.
func PersonPrompts(prompts []Prompt) []Prompt {
	var out []Prompt
	for _, p := range prompts {
		if p.Person() {
			out = append(out, p)
		}
	}
	return out
}

// BuildPrompts returns the prompts of a session's hook events, in their order, the agent's
// notices (PromptKindSystem) among them: the timeline shows them, the counts leave them out.
func BuildPrompts(events []telemetry.HookEvent) []Prompt {
	var prompts []Prompt
	for _, ev := range events {
		if ev.Event != "UserPromptSubmit" {
			continue
		}
		text, kind := PromptText(ev.Prompt)
		raw := []rune(ev.Prompt)
		if len(raw) > promptRawMax {
			raw = raw[:promptRawMax]
		}
		prompts = append(prompts, Prompt{
			At: ev.Time, Text: text, Kind: kind, TurnID: ev.TurnID, PromptID: ev.PromptID,
			Title: ev.SessionTitle, Raw: string(raw),
		})
	}
	return prompts
}
