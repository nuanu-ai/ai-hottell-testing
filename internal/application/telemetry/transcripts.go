//go:generate mockgen -source=transcripts.go -destination=./mock/transcripts.go -package mock

package telemetry

import (
	"context"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// TranscriptReader reads the transcript lines the binary sent. The store keeps every line it
// received, repeats included; the reader answers each line once.
type TranscriptReader interface {
	// TranscriptLines returns the lines of one file ordered by line number. key.UserID and
	// key.File are required: a key without either is an error, not telemetry.ErrUnavailable.
	// fromLine and toLine are line numbers, both included; lines count from 1, so 0 leaves that
	// end open.
	TranscriptLines(
		ctx context.Context, key telemetry.TranscriptKey, fromLine, toLine uint64,
	) (telemetry.Lines, error)
	// CodexFactLines returns the lines of one Codex rollout that CodexToolFacts and
	// CodexTurnFacts of the domain read, ordered by line number, as TranscriptLines does: the
	// event_msg records of the turn's boundaries, token counts and finished items, the
	// response_item records of calls and their outputs, turn_context and token_usage_record.
	// The other records, the messages and the reasoning among them, are not read, and an output
	// record comes back reduced, not as stored: its type, its call_id and, of the output, the
	// exit code and duration of the metadata of a JSON object, else the first 4096 characters of
	// its text. key.UserID and
	// key.File are required.
	CodexFactLines(ctx context.Context, key telemetry.TranscriptKey) (telemetry.Lines, error)
	// ClaudeUsageLines returns the lines of one Claude transcript that carry a model response's
	// tokens, the assistant records with message.usage, ordered by line number, as
	// TranscriptLines does. A line comes back reduced, not as stored: the record's type,
	// requestId, timestamp and isSidechain, and of the message its id, model and the four token
	// counts of the usage; the content stays in the store. key.UserID and key.File are required.
	ClaudeUsageLines(ctx context.Context, key telemetry.TranscriptKey) (telemetry.Lines, error)
	// ClaudeSessionUsage is ClaudeUsageLines over every transcript file of a Claude session in
	// one read, ordered by file, user and line, each (user, file, line) once. since, when not
	// zero, is the earliest stored time read, so that the days before the session are skipped;
	// userID uuid.Nil reads every person's.
	ClaudeSessionUsage(
		ctx context.Context, userID uuid.UUID, sessionID string, since time.Time,
	) ([]telemetry.SessionLine, error)
	// CodexFactsInPeriod is TranscriptFiles and CodexFactLines over every Codex session in one
	// read: the files and the fact lines of the records stored in [f.From, f.To), each with its
	// session, ordered by session, file and user, the lines then by line, each (user, session,
	// file, line) once; a file's kind and highest line are those of its records in the period.
	// f.UserID uuid.Nil reads every person's, f.Agent and f.SessionID are not read.
	// telemetry.ErrNoHookPeriod without a period.
	CodexFactsInPeriod(
		ctx context.Context, f telemetry.Filter,
	) ([]telemetry.SessionFile, []telemetry.SessionLine, error)
	// ClaudeUsageInPeriod is ClaudeSessionUsage over every Claude session in one read: the lines
	// stored in [f.From, f.To), each with its session, ordered by session, file, user and line,
	// each (user, session, file, line) once; f.UserID uuid.Nil reads every person's, f.Agent and
	// f.SessionID are not read. telemetry.ErrNoHookPeriod without a period.
	ClaudeUsageInPeriod(ctx context.Context, f telemetry.Filter) ([]telemetry.SessionLine, error)
	// ClaudeSourceRefs returns, in one read over every transcript file of a Claude session, the
	// lines a timeline event is built from: the assistant records with a tool_use, by its ids,
	// and the user records that are a prompt, by their promptId, never a tool's result. A line
	// comes back as its place and ids only; the text stays in the store. Ordered by file and
	// line, each (user, file, line) once; since and userID as in ClaudeSessionUsage.
	ClaudeSourceRefs(
		ctx context.Context, userID uuid.UUID, sessionID string, since time.Time,
	) ([]telemetry.SourceRef, error)
	// SourcePrefix returns the sha256, in hex, of the lines 1 to records of one file, each with a
	// "\n" after it: the source_sha256 hottell-local's session_source gives for a prefix of
	// records lines; records below 1 is an error. telemetry.ErrSourceIncomplete when the store
	// lacks one of those lines, and that is permanent, not a line still on its way, when the
	// binary never sends it: the inherited parent lines of a Codex subagent rollout, lines the
	// folder policy denied (the cwd of a Codex session changes per turn), history lines while
	// the history upload is off, and empty lines it skips. A file written with "\r\n" gives
	// another sum, since the stored lines carry no line break. The caller shows such a prefix
	// as not checked and does not retry.
	SourcePrefix(ctx context.Context, key telemetry.TranscriptKey, records int) (string, error)
	// TranscriptFiles returns the transcript files of a session ordered by name. userID uuid.Nil
	// reads every person's.
	TranscriptFiles(
		ctx context.Context, userID uuid.UUID, agent, sessionID string,
	) ([]telemetry.TranscriptFile, error)
}
