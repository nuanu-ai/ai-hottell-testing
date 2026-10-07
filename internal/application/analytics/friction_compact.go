package analytics

import "git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"

// CompactFriction returns the compact episodes of the session sid: one per compaction
// (Compactions), «сжатие контекста (<trigger>)».
func CompactFriction(sid string, compacts []telemetry.HookEvent) []FrictionEpisode {
	out := make([]FrictionEpisode, 0, len(compacts))
	for _, c := range compacts {
		trig := c.Trigger
		if trig == "" {
			trig = "триггер не указан"
		}
		out = append(out, FrictionEpisode{
			Evidence: []Evidence{frictionEvidence(sid, ISO(c.Time), "сжатие контекста ("+trig+")")},
		})
	}
	return out
}

// AbortFriction returns the abort episodes of the session sid: one per Interrupt hook, «ход
// прерван · <reason>». Claude Code records no Interrupt, so its sessions have none.
func AbortFriction(sid string, events []telemetry.HookEvent) []FrictionEpisode {
	var out []FrictionEpisode
	for _, ev := range events {
		if ev.Event != "Interrupt" {
			continue
		}
		reason := ev.Reason
		if reason == "" {
			reason = "Interrupt"
		}
		out = append(out, FrictionEpisode{
			Evidence: []Evidence{frictionEvidence(sid, ISO(ev.Time), "ход прерван · "+reason)},
		})
	}
	return out
}
