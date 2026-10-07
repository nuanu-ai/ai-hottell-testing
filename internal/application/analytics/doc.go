// Package analytics builds the live analytics of the agents' sessions on the server: what a
// session did, what it cost and where it stalled, from the hook events, the native OpenTelemetry
// of Claude Code and Codex and the transcript lines the collector stores.
//
// The package works on the types of internal/domain/telemetry (HookEvent, ClaudeEvent,
// ClaudeMetric, CodexSSE, CodexCoverage, TranscriptFile, Lines) and reads them through Source,
// which mirrors the readers of internal/application/telemetry. It keeps no record types of its
// own: what the readers answer is what the rules work on.
//
// A session is identified by the person, the agent and the session id together; times are UTC.
package analytics
