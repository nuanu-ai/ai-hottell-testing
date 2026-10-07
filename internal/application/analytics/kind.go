package analytics

import (
	"regexp"
	"slices"
	"strings"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// SessionKind is the kind of a session.
type SessionKind string

// The kinds of a session.
const (
	// SessionKindSystem: a service task of Codex itself, which the interface hides by default.
	SessionKindSystem SessionKind = "system"
	// SessionKindAutomation: a run on a schedule.
	SessionKindAutomation SessionKind = "automation"
	// SessionKindAgent: a session another agent started, a codex exec run or a lane an
	// orchestrator dispatched. It is listed but is not the person's sessions or time.
	SessionKindAgent SessionKind = "agent"
	// SessionKindUser: everything else, the person's work.
	SessionKindUser SessionKind = "user"
)

// systemPrompts are the first prompts of Codex's own service sessions and the reason each gives.
// Only explicit ones: a short session can be ordinary work, and one without prompts an incomplete
// recording, so neither is guessed to be a service session.
var systemPrompts = []struct { //nolint:gochecknoglobals // a fixed table
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`^# Overview Generate \d+ to \d+ hyperpersonalized suggestions`), "подсказки Codex Desktop"},
	{regexp.MustCompile(`^## Memory Writing Agent`), "память Codex"},
	{
		regexp.MustCompile(`^You are an expert at upholding safety and compliance standards for Codex ambient suggestions`),
		"проверка подсказок Codex",
	},
}

var (
	// systemCwdRe is Codex's own working folder, not a project.
	systemCwdRe = regexp.MustCompile(`/\.codex/memories(?:/|$)`)
	// automationPromptRe is the first prompt of a scheduled run.
	automationPromptRe = regexp.MustCompile(`^<heartbeat>`)
	// briefLineRe is the first line of an orchestrator's dispatch: it names a file in the folder
	// where orchestrators keep their briefs. A person who pastes such a path into a session of
	// their own starts it with other text or a pasted block, not with that line.
	briefLineRe = regexp.MustCompile(`/\.local/state/orchestrators/\S`)
)

// codexExecService is the service.name of Codex's native OpenTelemetry for a codex exec run.
const codexExecService = "codex_exec"

// CodexExec tells whether the Codex session of sse was run by codex exec: one of its native
// events comes from that service.
func CodexExec(sse []telemetry.CodexSSE) bool {
	return slices.ContainsFunc(sse, func(e telemetry.CodexSSE) bool { return e.Service == codexExecService })
}

// ClassifySession is the kind of a session and why, from its first typed prompt (a reply to the
// agent's question does not count) with its white space collapsed, its very first prompt, its
// working folder and whether codex exec ran it: SessionKindSystem for an explicit service prompt
// of Codex or the folder ~/.codex/memories; SessionKindAgent for a codex exec run, a session
// opened by another agent's cross-session message or by an orchestrator's brief; then
// SessionKindAutomation for a <heartbeat> prompt; SessionKindUser with no reason otherwise.
func ClassifySession(prompts []Prompt, cwd string, codexExec bool) (kind SessionKind, reason string) {
	var text string
	for _, p := range prompts {
		if p.Kind == PromptKindPrompt {
			text = strings.Join(strings.Fields(p.Raw), " ")
			break
		}
	}
	for _, sp := range systemPrompts {
		if sp.re.MatchString(text) {
			return SessionKindSystem, sp.reason
		}
	}
	if cwd != "" && systemCwdRe.MatchString(cwd) {
		return SessionKindSystem, "папка ~/.codex/memories"
	}
	if codexExec {
		return SessionKindAgent, "запуск codex exec"
	}
	if len(prompts) > 0 {
		first := strings.TrimSpace(prompts[0].Raw)
		if strings.HasPrefix(first, crossSessionTag) {
			return SessionKindAgent, "первая реплика — сообщение агента"
		}
		line, _, _ := strings.Cut(first, "\n")
		if briefLineRe.MatchString(line) {
			return SessionKindAgent, "первая реплика — бриф оркестратора"
		}
	}
	if automationPromptRe.MatchString(text) {
		return SessionKindAutomation, "реплика <heartbeat>"
	}
	return SessionKindUser, ""
}
