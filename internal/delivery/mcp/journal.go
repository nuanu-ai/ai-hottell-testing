package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CoachEvidence is a quote of a session a coach record rests on.
type CoachEvidence struct {
	Session string     `json:"session"`
	At      *time.Time `json:"at,omitempty"`
	Seq     *int       `json:"seq,omitempty"`
	Quote   string     `json:"quote"`
}

// CoachEntry is a coach record as the journal keeps it: a decision on a topic, or a check of
// a change (CheckOf). A read decision carries the fold of its last check (Result,
// Observations, Repeats, CheckedAt, Checks); the shape is hottell-local's coach_journal
// record without path (mcp.md «coach_journal»).
type CoachEntry struct {
	ID           string          `json:"id"`
	ClientRef    string          `json:"client_ref,omitempty"`
	CheckOf      string          `json:"check_of,omitempty"`
	TopicKey     string          `json:"topic_key"`
	At           time.Time       `json:"at"`
	Agent        string          `json:"agent"`
	Topic        string          `json:"topic,omitempty"`
	Findings     []string        `json:"findings,omitempty"`
	Evidence     []CoachEvidence `json:"evidence,omitempty"`
	Decision     string          `json:"decision,omitempty"`
	Layer        *string         `json:"layer"`
	Target       string          `json:"target,omitempty"`
	BeforeSHA256 string          `json:"before_sha256,omitempty"`
	AfterSHA256  string          `json:"after_sha256,omitempty"`
	Change       string          `json:"change,omitempty"`
	Rollback     string          `json:"rollback,omitempty"`
	Check        string          `json:"check,omitempty"`
	CheckAfter   string          `json:"check_after,omitempty"`
	Result       *string         `json:"result"`
	Observations *int            `json:"observations"`
	Repeats      *int            `json:"repeats,omitempty"`
	CheckedAt    *time.Time      `json:"checked_at,omitempty"`
	Checks       int             `json:"checks,omitempty"`
}

// CoachFilter narrows a read: one decision by ID, or the decisions of TopicKey; empty fields
// do not filter.
type CoachFilter struct {
	ID       string
	TopicKey string
}

// coachJournalInput is the input of coach_journal. entry is read raw (rawArgument): the
// journal's rules, not the schema, decide which of its fields a decision or a check needs,
// and name the field.
type coachJournalInput struct {
	Op       string `json:"op"`
	ID       string `json:"id"`
	TopicKey string `json:"topic_key"`
}

// coachJournalOutput is the result of coach_journal.
type coachJournalOutput struct {
	Count   int          `json:"count"`
	Entries []CoachEntry `json:"entries"`
}

// coachJournalInputSchema is the input schema of coach_journal as mcp.md gives it.
const coachJournalInputSchema = `{
  "type": "object",
  "required": ["op"],
  "additionalProperties": false,
  "properties": {
    "op": { "enum": ["read", "append"] },
    "id": { "type": "string", "description": "read: одно решение по id." },
    "topic_key": { "type": "string", "description": "read: решения одной темы." },
    "entry": {
      "type": "object",
      "description": "append: одна запись — решение по теме или проверка изменения (check_of). id и at ставит сервер.",
      "additionalProperties": false,
      "properties": {
        "client_ref": { "type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$", "description": "Ключ повтора, который задаёт клиент: повтор append с тем же ключом не пишет вторую запись." },
        "check_of": { "type": "string", "description": "Проверка: id решения applied или test." },
        "topic_key": { "type": "string", "description": "^[a-z0-9][a-z0-9-]{1,63}$; у проверки — topic_key её решения." },
        "agent": { "enum": ["codex", "claude"], "description": "Чьи сессии разбирались." },
        "topic": { "type": "string" },
        "findings": { "type": "array", "items": { "type": "string" }, "description": "Id находок ([A-Za-z0-9._:-], 1–128): p2:<20 hex>, live:…, friction:…." },
        "evidence": { "type": "array", "items": { "$ref": "#/$defs/evidence" } },
        "decision": { "enum": ["applied", "declined", "not_justified", "test"] },
        "layer": { "enum": ["experience", "instructions", "skill", "technical", null] },
        "target": { "type": "string" },
        "before_sha256": { "type": "string" },
        "after_sha256": { "type": "string" },
        "change": { "type": "string" },
        "rollback": { "type": "string" },
        "check": { "type": "string" },
        "check_after": { "type": "string", "description": "YYYY-MM-DD." },
        "result": { "enum": ["repeated", "not_repeated", "not_enough_data", null] },
        "observations": { "type": ["integer", "null"], "minimum": 0 },
        "repeats": { "type": "integer", "minimum": 1 }
      }
    }
  },
  "$defs": {
    "evidence": {
      "type": "object",
      "required": ["session", "quote"],
      "additionalProperties": false,
      "properties": {
        "session": { "type": "string" },
        "at": { "type": "string", "format": "date-time" },
        "seq": { "type": "integer", "minimum": 0, "description": "seq события session_read." },
        "quote": { "type": "string", "description": "Не длиннее 200 знаков после маскирования." }
      }
    }
  }
}`

// coachJournalOutputSchema is the output schema of coach_journal as mcp.md gives it.
const coachJournalOutputSchema = `{
  "type": "object",
  "required": ["count", "entries"],
  "additionalProperties": false,
  "properties": {
    "count": { "type": "integer", "minimum": 0 },
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "topic_key", "at", "agent"],
        "additionalProperties": false,
        "properties": {
          "id": { "type": "string", "description": "record_id записи журнала." },
          "client_ref": { "type": "string", "description": "Ключ повтора, если клиент его задал." },
          "check_of": { "type": "string" },
          "topic_key": { "type": "string" },
          "at": { "type": "string", "format": "date-time", "description": "recorded_at." },
          "agent": { "enum": ["codex", "claude"] },
          "topic": { "type": "string" },
          "findings": { "type": "array", "items": { "type": "string" } },
          "evidence": { "type": "array", "items": { "$ref": "#/$defs/evidence" } },
          "decision": { "enum": ["applied", "declined", "not_justified", "test"] },
          "layer": { "enum": ["experience", "instructions", "skill", "technical", null] },
          "target": { "type": "string" },
          "before_sha256": { "type": "string" },
          "after_sha256": { "type": "string" },
          "change": { "type": "string" },
          "rollback": { "type": "string" },
          "check": { "type": "string" },
          "check_after": { "type": "string" },
          "result": { "enum": ["repeated", "not_repeated", "not_enough_data", null] },
          "observations": { "type": ["integer", "null"] },
          "repeats": { "type": "integer" },
          "checked_at": { "type": "string", "format": "date-time", "description": "read: at последней проверки этого решения." },
          "checks": { "type": "integer", "minimum": 1, "description": "read: сколько проверок у решения." }
        }
      }
    }
  },
  "$defs": {
    "evidence": {
      "type": "object",
      "required": ["session", "quote"],
      "additionalProperties": false,
      "properties": {
        "session": { "type": "string" },
        "at": { "type": "string", "format": "date-time" },
        "seq": { "type": "integer", "minimum": 0 },
        "quote": { "type": "string" }
      }
    }
  }
}`

// errNoEntry is the answer to an append without an entry.
var errNoEntry = &ToolError{Code: CodeInvalid, Message: "entry: append needs an entry"}

// addCoachJournal adds coach_journal, the coach's decisions in the server's decision journal
// (it moved here from hottell-local). read returns the key's user's decisions with their last
// check folded in; append writes one decision or check and returns it as written.
func addCoachJournal(server *sdkmcp.Server, journal Journal, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "coach_journal",
		Description: "Журнал решений коуча на сервере hottell; запись не меняет настройки человека. " +
			"op=read — решения (все или одно по id/topic_key), у каждого свёрнута последняя проверка " +
			"(result, observations, repeats, checked_at, checks). op=append — одна запись: решение по теме " +
			"(applied, declined, not_justified, test) или проверка изменения (check_of); id и at ставит сервер, " +
			"client_ref защищает от двойной записи при повторе. Секреты и домашний каталог маскируются, " +
			"цитаты — не длиннее 200 знаков.",
		InputSchema:  json.RawMessage(coachJournalInputSchema),
		OutputSchema: json.RawMessage(coachJournalOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in coachJournalInput) (
		*sdkmcp.CallToolResult, coachJournalOutput, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "coach_journal: request user", "error", err)
			return nil, coachJournalOutput{}, errInternal
		}
		if in.Op == "append" {
			entry, err := rawArgument(req, "entry")
			if err != nil {
				logger.ErrorContext(ctx, "coach_journal: raw entry", "error", err)
				return nil, coachJournalOutput{}, errInternal
			}
			if len(entry) == 0 || string(entry) == "null" {
				return nil, coachJournalOutput{}, errNoEntry
			}
			written, err := journal.CoachAppend(ctx, userID, entry)
			if err != nil {
				return nil, coachJournalOutput{}, toolFailure(ctx, logger, "coach_journal: append", err)
			}
			return nil, coachJournalOutput{Count: 1, Entries: []CoachEntry{utcEntry(written)}}, nil
		}
		entries, err := journal.CoachRead(ctx, userID, CoachFilter{ID: in.ID, TopicKey: in.TopicKey})
		if err != nil {
			return nil, coachJournalOutput{}, toolFailure(ctx, logger, "coach_journal: read", err)
		}
		out := coachJournalOutput{Count: len(entries), Entries: make([]CoachEntry, 0, len(entries))}
		for _, e := range entries {
			out.Entries = append(out.Entries, utcEntry(e))
		}
		return nil, out, nil
	})
}

// utcEntry writes the times of e in UTC, as every time of the answers.
func utcEntry(e CoachEntry) CoachEntry {
	e.At = e.At.UTC()
	if e.CheckedAt != nil {
		at := e.CheckedAt.UTC()
		e.CheckedAt = &at
	}
	for i, ev := range e.Evidence {
		if ev.At != nil {
			at := ev.At.UTC()
			e.Evidence[i].At = &at
		}
	}
	return e
}
