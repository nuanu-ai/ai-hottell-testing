package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// SrcClaudeTranscript is a model response of Claude Code read from its transcript, for a session
// without native OpenTelemetry.
const SrcClaudeTranscript = "claude_transcript"

// GapClaudeTranscript is the gap of a dataset with a Claude session whose tokens come from its
// transcript.
const GapClaudeTranscript = "Claude Code без нативного OTel: токены — из транскрипта, стоимость — оценка по цене API модели."

// claudeUsageLine is what ClaudeSessionUsage hands back of an assistant line.
type claudeUsageLine struct {
	RequestID string `json:"requestId"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input         int64 `json:"input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			Output        int64 `json:"output_tokens"`
			// ByTTL splits the cache write by the cache's lifetime; zero when the line has no
			// split (HT-517).
			ByTTL struct {
				FiveMin int64 `json:"ephemeral_5m_input_tokens"`
				OneHour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// TranscriptSlack is how long before a session's first hook event its transcript lines are still
// read: a line is stored when the binary reads it, which is after the session started, so a day
// leaves room for a clock and a late first hook without reading the history before.
const TranscriptSlack = 24 * time.Hour

// ClaudeTranscriptAPI returns the model requests of a Claude session from the usage lines of its
// transcript files, main and subagents alike, when the session has no api_request among its
// native events; with one it returns nothing, since the native OpenTelemetry already gives the
// tokens. A line stored before start, the session's first hook event, less TranscriptSlack is
// left out (a zero start keeps every line) (HT-389, HT-453). Claude writes one response in several
// lines, so the lines of one requestId (else of one message id) are one request, with the usage
// of the last of them. A record's cost is the estimate at its model's price, nil for a model
// without one (HT-500).
func ClaudeTranscriptAPI(native []telemetry.ClaudeEvent, lines []telemetry.SessionLine, start time.Time) []APIRecord {
	if hasAPIRequest(native) {
		return nil
	}
	return claudeUsageAPI(parseClaudeUsage(lines), start)
}

// claudeUsage is one usage line of a Claude transcript as ClaudeTranscriptAPI reads it, its body
// parsed: the person of its file, when it was stored, the id of its request (else of its
// message) and the request it gives. The usage lines of a period are kept so, not as their
// bodies, while the period's sessions are built (HT-536).
type claudeUsage struct {
	user   uuid.UUID
	stored time.Time
	id     string
	rec    APIRecord
}

// parseClaudeUsage parses lines in their order; a line whose body is not a usage line is left out.
func parseClaudeUsage(lines []telemetry.SessionLine) []claudeUsage {
	out := make([]claudeUsage, 0, len(lines))
	for i := range lines {
		if u, ok := parseClaudeUsageLine(&lines[i]); ok {
			out = append(out, u)
		}
	}
	return out
}

// parseClaudeUsageLine parses one usage line; false when its body is not one.
func parseClaudeUsageLine(l *telemetry.SessionLine) (claudeUsage, bool) {
	var u claudeUsageLine
	if json.Unmarshal([]byte(l.Body), &u) != nil {
		return claudeUsage{}, false
	}
	id := u.RequestID
	if id == "" {
		id = u.Message.ID
	}
	return claudeUsage{user: l.UserID, stored: l.Time, id: id, rec: claudeTranscriptRecord(u, l.Time)}, true
}

// claudeUsageAPI is ClaudeTranscriptAPI over the parsed usage lines of a session without an
// api_request.
func claudeUsageAPI(lines []claudeUsage, start time.Time) []APIRecord {
	since := transcriptSince(start)
	var api []APIRecord
	byRequest := map[string]int{}
	for _, l := range lines {
		if l.stored.Before(since) {
			continue
		}
		rec := l.rec
		if i, seen := byRequest[l.id]; seen && l.id != "" {
			rec.At = api[i].At
			api[i] = rec
			continue
		}
		byRequest[l.id] = len(api)
		api = append(api, rec)
	}
	slices.SortStableFunc(api, func(a, b APIRecord) int { return a.At.Compare(b.At) })
	return api
}

// hasAPIRequest reports whether the native events of a Claude session hold an api_request.
func hasAPIRequest(native []telemetry.ClaudeEvent) bool {
	return slices.ContainsFunc(native, func(e telemetry.ClaudeEvent) bool { return e.Event == "api_request" })
}

// transcriptSince is the earliest stored time of a transcript line of a session whose first hook
// event is at start; zero for a zero start.
func transcriptSince(start time.Time) time.Time {
	if start.IsZero() {
		return time.Time{}
	}
	return start.Add(-TranscriptSlack)
}

// readClaudeUsage reads, in one read, the Claude usage lines of every session whose transcript
// may fall in period: stored in [period.From − TranscriptSlack, period.To), laid out by session id
// in the order of the read, file and person (HT-453), each parsed as it is laid out, so that the
// period's bodies are let go once read (HT-536).
func readClaudeUsage(ctx context.Context, src Source, period telemetry.Filter) (map[string][]claudeUsage, error) {
	lines, err := src.ClaudeUsageInPeriod(ctx, telemetry.Filter{
		UserID: period.UserID, From: period.From.Add(-TranscriptSlack), To: period.To,
	})
	if err != nil {
		return nil, fmt.Errorf("read claude usage lines: %w", err)
	}
	usage := map[string][]claudeUsage{}
	for i := range lines {
		if u, ok := parseClaudeUsageLine(&lines[i]); ok {
			usage[lines[i].SessionID] = append(usage[lines[i].SessionID], u)
		}
	}
	return usage, nil
}

// sessionTranscriptAPI is ClaudeTranscriptAPI of the session key over usage, the lines read for
// the whole period, or, when usage is nil, over the lines of the session read alone. As in that
// read, a key without a person takes the lines of every person's files.
func sessionTranscriptAPI(
	ctx context.Context, src Source, key SessionKey, native []telemetry.ClaudeEvent, start time.Time,
	usage map[string][]claudeUsage,
) ([]APIRecord, error) {
	if hasAPIRequest(native) {
		return nil, nil
	}
	if usage != nil {
		lines := usage[key.SessionID]
		if key.UserID != uuid.Nil {
			lines = slices.DeleteFunc(slices.Clone(lines), func(l claudeUsage) bool { return l.user != key.UserID })
		}
		return claudeUsageAPI(lines, start), nil
	}
	lines, err := src.ClaudeSessionUsage(ctx, key.UserID, key.SessionID, transcriptSince(start))
	if err != nil {
		return nil, fmt.Errorf("read claude usage lines: %w", err)
	}
	return ClaudeTranscriptAPI(native, lines, start), nil
}

// claudeTranscriptRecord is the request of one usage line; at is the line's time when the line
// has no timestamp of its own.
func claudeTranscriptRecord(u claudeUsageLine, at time.Time) APIRecord {
	if t, err := time.Parse(time.RFC3339Nano, u.Timestamp); err == nil {
		at = t.UTC()
	}
	us := u.Message.Usage
	return APIRecord{
		At: at, Model: u.Message.Model, Input: us.Input + us.CacheRead + us.CacheCreation,
		Cached: us.CacheRead, Created: us.CacheCreation, Output: us.Output, Src: SrcClaudeTranscript,
		Cost: claudeTokensCost(u.Message.Model, claudeTokens{
			Input: us.Input + us.CacheRead + us.CacheCreation, CacheRead: us.CacheRead,
			CacheCreation: us.CacheCreation, CacheCreation1h: us.ByTTL.OneHour, Output: us.Output,
		}),
	}
}

// ClaudeTranscriptGap is GapClaudeTranscript when a session's requests came from its transcript.
func ClaudeTranscriptGap(api []APIRecord) (string, bool) {
	if slices.ContainsFunc(api, func(a APIRecord) bool { return a.Src == SrcClaudeTranscript }) {
		return GapClaudeTranscript, true
	}
	return "", false
}
