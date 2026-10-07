//go:generate mockgen -source=external.go -destination=./mock/external.go -package mock

package analytics

import (
	"context"
	"encoding/json"
	"iter"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// Source is everything analytics reads. The implementation is the stage-1 ClickHouse read
// adapter: its readers (HookReader, NativeReader and TranscriptReader of
// internal/application/telemetry) already have these methods, with these signatures, and
// satisfy Source together without more code. The adapter computes the digest of a tool
// response, its head, tail, length and hash, in SQL (cityHash64), so that heavy responses never
// leave ClickHouse: a HookEvent comes with the digest, not with the whole output.
//
// The query is telemetry.Filter. The hook reads always need a period [From, To), To excluded, and
// answer telemetry.ErrNoHookPeriod without one, a SessionID notwithstanding. The native reads
// need a period or a SessionID and answer telemetry.ErrNoNativePeriod without either. There is
// no whole-history read, so analytics always passes the period it works on. Every method answers telemetry.ErrUnavailable while the store cannot
// be read.
type Source interface {
	// HookEventSeq yields the hook events matching the filter, oldest first, as the store reads
	// them, so that the events of a period never sit in one slice; an error ends it as its last
	// pair, and the events yielded before it are of the read.
	HookEventSeq(ctx context.Context, filter telemetry.Filter) iter.Seq2[telemetry.HookEvent, error]
	// Pulse returns the hook events of the ten minutes before q.Now by 10 seconds and agent, and
	// the sessions active in the two minutes before it, of the person and agent of q when set.
	Pulse(ctx context.Context, q telemetry.PulseQuery) (telemetry.Pulse, error)
	// ActivityPulse returns, in one read, the hook events of each person by hour over [from, to),
	// one count per hour from from, the empty hours 0; the people without an event are absent.
	ActivityPulse(ctx context.Context, from, to time.Time) (map[uuid.UUID][]int, error)
	// SessionPrompts returns, in one read, the UserPromptSubmit events of the sessions stored in
	// [from, to), oldest first, each with only its time, person, agent, session and prompt.
	SessionPrompts(
		ctx context.Context, sessions []telemetry.ActiveSession, from, to time.Time,
	) ([]telemetry.HookEvent, error)
	// SkillSnapshot returns the latest skill snapshot of the session of the person and agent;
	// false when there is none.
	SkillSnapshot(
		ctx context.Context, userID uuid.UUID, agent, sessionID string,
	) (telemetry.SkillSnapshot, bool, error)
	// SkillSnapshotsInPeriod returns, in one read, the latest skill snapshot of every session
	// with a snapshot stored in [f.From, f.To), ordered by person, agent and session; f.UserID
	// and f.Agent narrow it when set; a damaged snapshot is an entry with Err set, not an error
	// of the read. telemetry.ErrNoSessionPeriod without a period.
	SkillSnapshotsInPeriod(ctx context.Context, f telemetry.Filter) ([]telemetry.SessionSkillSnapshot, error)
	// SkillSnapshotsOfSessions returns, in one read and with no period, the latest skill
	// snapshot of every person and agent of the sessions sessionIDs, as SkillSnapshotsInPeriod
	// returns them; no ids read nothing.
	SkillSnapshotsOfSessions(ctx context.Context, sessionIDs []string) ([]telemetry.SessionSkillSnapshot, error)
	// ClaudeEvents returns the native events of Claude Code (api_request, api_error,
	// tool_result, tool_decision, user_prompt), oldest first.
	ClaudeEvents(ctx context.Context, f telemetry.Filter) ([]telemetry.ClaudeEvent, error)
	// ClaudeMetrics returns the claude_code.* data points, oldest first.
	ClaudeMetrics(ctx context.Context, f telemetry.Filter) ([]telemetry.ClaudeMetric, error)
	// CodexSSE returns the codex.sse_event events that carry a conversation.id, oldest first.
	CodexSSE(ctx context.Context, f telemetry.Filter) ([]telemetry.CodexSSE, error)
	// CodexCoverage counts the Codex events by service, event and link to a session.
	CodexCoverage(ctx context.Context, f telemetry.Filter) ([]telemetry.CodexCoverage, error)
	// TranscriptFiles returns the transcript files of a session ordered by name. userID
	// uuid.Nil reads every person's.
	TranscriptFiles(
		ctx context.Context, userID uuid.UUID, agent, sessionID string,
	) ([]telemetry.TranscriptFile, error)
	// CodexFactLines returns the lines of one Codex rollout that the Codex facts of the
	// domain read, ordered by line number. key.UserID and key.File are required.
	CodexFactLines(ctx context.Context, key telemetry.TranscriptKey) (telemetry.Lines, error)
	// CodexFactsInPeriod returns, in one read over every Codex session, the transcript files and
	// the lines CodexFactLines returns of the records stored in [f.From, f.To), each with its
	// session, ordered by session, file and user, the lines then by line, each once; f.UserID
	// uuid.Nil reads every person's.
	CodexFactsInPeriod(
		ctx context.Context, f telemetry.Filter,
	) ([]telemetry.SessionFile, []telemetry.SessionLine, error)
	// ClaudeSessionUsage returns, in one read, the assistant lines of every transcript file of
	// a Claude session that carry message.usage, reduced to the request id, time, side-chain
	// flag, message id, model and token counts, ordered by file, user and line, each once.
	// since, when not zero, is the earliest stored time read; userID uuid.Nil reads every
	// person's.
	ClaudeSessionUsage(
		ctx context.Context, userID uuid.UUID, sessionID string, since time.Time,
	) ([]telemetry.SessionLine, error)
	// ClaudeUsageInPeriod returns, in one read, the lines ClaudeSessionUsage returns of every
	// Claude session, stored in [f.From, f.To), each with its session, ordered by session, file,
	// user and line, each once; f.UserID uuid.Nil reads every person's.
	ClaudeUsageInPeriod(ctx context.Context, f telemetry.Filter) ([]telemetry.SessionLine, error)
	// ClaudeSourceRefs returns the lines of every transcript file of a Claude session a timeline
	// event is built from: the assistant lines with a tool_use by its ids and the user lines that
	// are a prompt by their promptId, no text; ordered by file and line, each once. since and
	// userID as in ClaudeSessionUsage.
	ClaudeSourceRefs(
		ctx context.Context, userID uuid.UUID, sessionID string, since time.Time,
	) ([]telemetry.SourceRef, error)
}

// Users names the people of the dataset.
type Users interface {
	// Names returns the name of each of ids that is a person; an id with no person is absent.
	Names(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

// Clock tells the time.
type Clock interface {
	// Now returns the current time in UTC.
	Now() time.Time
}

// Keys reads the access keys of a person.
type Keys interface {
	// Latest returns the person's key of kind that tells the state of the sending: the active
	// one, else the last one revoked; domain.ErrAccessKeyNotFound when the person never had one.
	Latest(ctx context.Context, userID uuid.UUID, kind domain.AccessKeyKind) (domain.AccessKey, error)
}

// Received tells when the store last took a person's records.
type Received interface {
	// LastReceived returns, for each agent and source of the person's records taken at or after
	// since, when the last of them arrived.
	LastReceived(ctx context.Context, userID uuid.UUID, since time.Time) ([]telemetry.LastReceived, error)
}

// Settings reads the stored telemetry settings of a person.
type Settings interface {
	// Get returns the person's settings document and its version, 0 when never saved.
	Get(ctx context.Context, userID uuid.UUID) (json.RawMessage, int64, error)
}

// HiddenTopicsStore keeps the topics each person hid.
type HiddenTopicsStore interface {
	// Hide hides key for the person at at; hiding a hidden topic keeps its first time.
	Hide(ctx context.Context, userID uuid.UUID, key string, at time.Time) error
	// Unhide shows key to the person again; a topic not hidden is no error.
	Unhide(ctx context.Context, userID uuid.UUID, key string) error
	// UnhideAll shows every topic the person hid again.
	UnhideAll(ctx context.Context, userID uuid.UUID) error
	// List returns the topics the person hid, the earliest hidden first.
	List(ctx context.Context, userID uuid.UUID) ([]string, error)
}

// DeliveryReports reads the last delivery report of a person's hottell binary.
type DeliveryReports interface {
	// Get returns the person's last report, false when the binary never sent one.
	Get(ctx context.Context, userID uuid.UUID) (domain.DeliveryStatus, bool, error)
}
