package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolError is an error a port of the Deep, journal and registry tools returns for the agent
// to see and act on: Code is one of the codes of mcp.md «Инструменты разбора и журнала»
// (invalid, not_found, conflict, too_large) and Message the text after it. Any other error of
// a port is internal: it is logged and the agent gets errInternal.
type ToolError struct {
	Code    string
	Message string
}

// Error is the text the agent sees: <code>: <message>.
func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// Codes of ToolError.
const (
	CodeInvalid  = "invalid"
	CodeNotFound = "not_found"
	CodeConflict = "conflict"
	CodeTooLarge = "too_large"
)

// toolFailure is the error a tool returns for err of a port: a ToolError as is, anything else
// logged under what and answered errInternal.
func toolFailure(ctx context.Context, logger *slog.Logger, what string, err error) error {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr
	}
	logger.ErrorContext(ctx, what, "error", err)
	return errInternal
}

// DeepTarget is where a proposal's change goes.
type DeepTarget struct {
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
	Version string `json:"version"`
}

// DeepPreviousReport is the last published version of the report of a session.
type DeepPreviousReport struct {
	CandidateID     string          `json:"candidate_id"`
	CandidateSHA256 string          `json:"candidate_sha256"`
	SourceSHA256    string          `json:"source_sha256"`
	SourceRecords   int             `json:"source_records"`
	PublishedAt     time.Time       `json:"published_at"`
	Report          json.RawMessage `json:"report"`
}

// DeepCorpusTask is a task of a published report, as continued_from and peer_sources cite it.
type DeepCorpusTask struct {
	TaskID    string `json:"task_id"`
	Goal      string `json:"goal"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Outcome   string `json:"outcome"`
}

// DeepCorpusReport is a published report of the user: CandidateSHA256 is its published
// version, the deep_report_sha256 of a skill opportunities report's corpus.
type DeepCorpusReport struct {
	SessionID       string           `json:"session_id"`
	SourceSHA256    string           `json:"source_sha256"`
	SourceRecords   int              `json:"source_records"`
	CandidateSHA256 string           `json:"candidate_sha256"`
	Tasks           []DeepCorpusTask `json:"tasks"`
}

// DeepApplied is a registry proposal of the user whose change is applied.
type DeepApplied struct {
	ProposalID  string     `json:"proposal_id"`
	Fingerprint string     `json:"fingerprint"`
	Target      DeepTarget `json:"target"`
	Change      string     `json:"change"`
	At          time.Time  `json:"at"`
}

// DeepProposal is a registry proposal of the user, for the agent to reuse its change_intent.
type DeepProposal struct {
	ProposalID   string `json:"proposal_id"`
	PatternID    string `json:"pattern_id"`
	Scope        string `json:"scope"`
	ChangeIntent string `json:"change_intent"`
	Readiness    string `json:"readiness"`
}

// DeepContext is what an agent needs before the retro of a session, or, without a session,
// the user's whole published corpus for a skill opportunities report.
type DeepContext struct {
	// PreviousReport is nil when the session has no published report or none was named.
	PreviousReport *DeepPreviousReport
	// Corpus are the published reports of the user but the named session, by session_id.
	Corpus    []DeepCorpusReport
	Applied   []DeepApplied
	Proposals []DeepProposal
}

// DeepCandidate is a version of a session's report as the reviewer gets it.
type DeepCandidate struct {
	CandidateID     string          `json:"candidate_id"`
	SessionID       string          `json:"session_id"`
	CandidateSHA256 string          `json:"candidate_sha256"`
	Status          string          `json:"status"`
	SourceSHA256    string          `json:"source_sha256"`
	SourceRecords   int             `json:"source_records"`
	SourceCheck     string          `json:"source_check"`
	SubmittedAt     time.Time       `json:"submitted_at"`
	Report          json.RawMessage `json:"report"`
}

// deepContextInput is the input of deep_context; session_id may be left out.
type deepContextInput struct {
	SessionID string `json:"session_id"`
}

// deepContextOutput is the result of deep_context; the lists are never null, session_id is
// null when the input named none.
type deepContextOutput struct {
	SessionID      *string             `json:"session_id"`
	PreviousReport *DeepPreviousReport `json:"previous_report"`
	Corpus         []DeepCorpusReport  `json:"corpus"`
	Applied        []DeepApplied       `json:"applied"`
	Proposals      []DeepProposal      `json:"proposals"`
}

const deepContextInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "session_id": { "type": "string", "minLength": 1, "description": "Id сессии, которую агент разбирает: session_id из session_source. Без него — весь опубликованный корпус для отчёта о возможностях skills." }
  }
}`

// deepContextOutputSchema is the output schema of deep_context as mcp.md gives it.
const deepContextOutputSchema = `{
  "type": "object",
  "required": ["session_id", "previous_report", "corpus", "applied", "proposals"],
  "additionalProperties": false,
  "properties": {
    "session_id": { "type": ["string", "null"], "description": "session_id из входа; null, если его не передали." },
    "previous_report": {
      "description": "Последняя опубликованная версия отчёта этой сессии; null — опубликованной нет или session_id не передан. Предмет проверки, а не источник истины.",
      "oneOf": [
        { "type": "null" },
        {
          "type": "object",
          "required": ["candidate_id", "candidate_sha256", "source_sha256", "source_records", "published_at", "report"],
          "additionalProperties": false,
          "properties": {
            "candidate_id": { "type": "string" },
            "candidate_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
            "source_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
            "source_records": { "type": "integer", "minimum": 1 },
            "published_at": { "type": "string", "format": "date-time" },
            "report": { "type": "object", "description": "Отчёт Deep v2, маскированный, как он хранится." }
          }
        }
      ]
    },
    "corpus": {
      "type": "array",
      "description": "Опубликованные отчёты пользователя по возрастанию session_id, кроме сессии из входа (без session_id — все): на них ссылаются continued_from и peer_sources, из них строится corpus отчёта о возможностях skills.",
      "items": {
        "type": "object",
        "required": ["session_id", "source_sha256", "source_records", "candidate_sha256", "tasks"],
        "additionalProperties": false,
        "properties": {
          "session_id": { "type": "string" },
          "source_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
          "candidate_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "candidate_sha256 опубликованной версии: deep_report_sha256 в corpus отчёта о возможностях skills." },
          "source_records": { "type": "integer", "minimum": 1 },
          "tasks": {
            "type": "array",
            "items": {
              "type": "object",
              "required": ["task_id", "goal", "start_line", "end_line", "outcome"],
              "additionalProperties": false,
              "properties": {
                "task_id": { "type": "string" },
                "goal": { "type": "string" },
                "start_line": { "type": "integer", "minimum": 1 },
                "end_line": { "type": "integer", "minimum": 1 },
                "outcome": { "enum": ["verified", "partial", "failed", "unknown"] }
              }
            }
          }
        }
      }
    },
    "applied": {
      "type": "array",
      "description": "Предложения реестра пользователя с execution.status = applied (событие application или запись коуча applied/test): такую правку не предлагать повторно — deep_submit её отклонит.",
      "items": {
        "type": "object",
        "required": ["proposal_id", "fingerprint", "target", "change", "at"],
        "additionalProperties": false,
        "properties": {
          "proposal_id": { "type": "string", "pattern": "^p2:[0-9a-f]{20}$" },
          "fingerprint": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "proposal_fingerprint применённой версии: sha256(canonical({group_key, target, change}))." },
          "target": { "$ref": "#/$defs/target" },
          "change": { "type": "string" },
          "at": { "type": "string", "format": "date-time", "description": "execution.at." }
        }
      }
    },
    "proposals": {
      "type": "array",
      "description": "Все предложения реестра пользователя.",
      "items": {
        "type": "object",
        "required": ["proposal_id", "pattern_id", "scope", "change_intent", "readiness"],
        "additionalProperties": false,
        "properties": {
          "proposal_id": { "type": "string", "pattern": "^p2:[0-9a-f]{20}$" },
          "pattern_id": { "type": "string" },
          "scope": { "type": "string" },
          "change_intent": { "type": "string" },
          "readiness": { "enum": ["hypothesis", "needs_specification", "prepared_verified"], "description": "readiness.status." }
        }
      }
    }
  },
  "$defs": {
    "target": {
      "type": "object",
      "required": ["kind", "locator", "version"],
      "additionalProperties": false,
      "properties": { "kind": { "type": "string" }, "locator": { "type": "string" }, "version": { "type": "string" } }
    }
  }
}`

// deepCandidateGetInput is the input of deep_candidate_get.
type deepCandidateGetInput struct {
	CandidateID string `json:"candidate_id"`
}

const deepCandidateGetInputSchema = `{
  "type": "object",
  "required": ["candidate_id"],
  "additionalProperties": false,
  "properties": {
    "candidate_id": { "type": "string", "minLength": 1 }
  }
}`

// deepCandidateGetOutputSchema is the output schema of deep_candidate_get as mcp.md gives it.
const deepCandidateGetOutputSchema = `{
  "type": "object",
  "required": ["candidate_id", "session_id", "candidate_sha256", "status", "source_sha256", "source_records", "source_check", "submitted_at", "report"],
  "additionalProperties": false,
  "properties": {
    "candidate_id": { "type": "string" },
    "session_id": { "type": "string" },
    "candidate_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Ровно это значение проверка пишет в review.candidate_sha256." },
    "status": { "enum": ["candidate", "published", "superseded"] },
    "source_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
    "source_records": { "type": "integer", "minimum": 1 },
    "source_check": { "enum": ["matched", "not_checked", "mismatch"] },
    "submitted_at": { "type": "string", "format": "date-time" },
    "report": { "type": "object", "description": "Маскированный отчёт Deep v2, по которому посчитан candidate_sha256." }
  }
}`

// errNoCandidate is the answer for a candidate the user does not have, theirs or not.
var errNoCandidate = &ToolError{Code: CodeNotFound, Message: "candidate_id: no such candidate"}

// addDeepRead adds deep_context and deep_candidate_get: what an agent needs before a retro,
// and the exact candidate a reviewer checks. Both read only the data of the key's user.
func addDeepRead(server *sdkmcp.Server, deep Deep, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "deep_context",
		Description: "Всё, что нужно до глубокого разбора сессии: прежний опубликованный отчёт этой сессии, " +
			"опубликованный корпус для continued_from и peer_sources, уже применённые правки (их не предлагать) " +
			"и предложения реестра (брать ту же формулировку change_intent). Без session_id — весь опубликованный " +
			"корпус для corpus отчёта о возможностях skills.",
		Annotations:  &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema:  json.RawMessage(deepContextInputSchema),
		OutputSchema: json.RawMessage(deepContextOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deepContextInput) (
		*sdkmcp.CallToolResult, deepContextOutput, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "deep_context: request user", "error", err)
			return nil, deepContextOutput{}, errInternal
		}
		got, err := deep.Context(ctx, userID, in.SessionID)
		if err != nil {
			return nil, deepContextOutput{}, toolFailure(ctx, logger, "deep_context: read the context", err)
		}
		var sessionID *string
		if in.SessionID != "" {
			sessionID = &in.SessionID
		}
		return nil, deepContextOutput{
			SessionID:      sessionID,
			PreviousReport: got.PreviousReport,
			Corpus:         nonNil(got.Corpus),
			Applied:        nonNil(got.Applied),
			Proposals:      nonNil(got.Proposals),
		}, nil
	})

	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "deep_candidate_get",
		Description: "Кандидат отчёта для независимой проверки: точный документ и его candidate_sha256, " +
			"который проверка пишет в review.candidate_sha256.",
		Annotations:  &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema:  json.RawMessage(deepCandidateGetInputSchema),
		OutputSchema: json.RawMessage(deepCandidateGetOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deepCandidateGetInput) (
		*sdkmcp.CallToolResult, DeepCandidate, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "deep_candidate_get: request user", "error", err)
			return nil, DeepCandidate{}, errInternal
		}
		// A candidate id is a UUID: anything else names no candidate of anybody.
		candidateID, err := uuid.Parse(in.CandidateID)
		if err != nil {
			return nil, DeepCandidate{}, errNoCandidate
		}
		got, err := deep.Candidate(ctx, userID, candidateID)
		if err != nil {
			return nil, DeepCandidate{}, toolFailure(ctx, logger, "deep_candidate_get: read the candidate", err)
		}
		got.SubmittedAt = got.SubmittedAt.UTC()
		return nil, got, nil
	})
}

// rawArgument returns the argument name as the client wrote it. go-sdk validates the arguments
// by decoding them into a map and encoding them again, which turns 1.0 into 1; pkg/deepv2 tells
// the two apart, so a free object (report, review, entry, detail, inventory) is taken from the
// request's own arguments (mcp.md «Свободные объекты — сырым JSON»).
func rawArgument(req *sdkmcp.CallToolRequest, name string) (json.RawMessage, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return nil, fmt.Errorf("decode the arguments: %w", err)
	}
	return args[name], nil
}

// nonNil turns a nil list into an empty one: the schemas want arrays, not null.
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// maxReportBytes bounds the raw JSON of a report: a larger one is too_large, unchecked.
const maxReportBytes = 2 << 20

// DeepSubmission is a candidate report as the agent sends it: Report is the raw JSON of the
// arguments, for pkg/deepv2's Decode to keep its numbers as written.
type DeepSubmission struct {
	SessionID string
	// Agent and Path are session_source's: the service compares the prefix with the lines the
	// binary sent by the agent and the file name.
	Agent         string
	Path          string
	SourceSHA256  string
	SourceRecords int
	Report        json.RawMessage
}

// DeepSubmitted is the stored candidate: a new one, or the version a repeat matched.
type DeepSubmitted struct {
	CandidateID     string `json:"candidate_id"`
	CandidateSHA256 string `json:"candidate_sha256"`
	Status          string `json:"status"`
	SourceCheck     string `json:"source_check"`
}

// DeepPublished is the answer to an approving review: the published candidate and how many
// proposals of the user's registry the rebuild changed.
type DeepPublished struct {
	Status           string `json:"status"`
	CandidateID      string `json:"candidate_id"`
	CandidateSHA256  string `json:"candidate_sha256"`
	ProposalsChanged int    `json:"proposals_changed"`
}

// deepSubmitInput is the input of deep_submit; report is read raw (rawArgument).
type deepSubmitInput struct {
	SessionID     string `json:"session_id"`
	Agent         string `json:"agent"`
	Path          string `json:"path"`
	SourceSHA256  string `json:"source_sha256"`
	SourceRecords int    `json:"source_records"`
}

// deepSubmitInputSchema is the input schema of deep_submit as mcp.md gives it.
const deepSubmitInputSchema = `{
  "type": "object",
  "required": ["session_id", "agent", "path", "source_sha256", "source_records", "report"],
  "additionalProperties": false,
  "properties": {
    "session_id": { "type": "string", "minLength": 1, "description": "session_id из session_source." },
    "agent": { "enum": ["codex", "claude"], "description": "agent из session_source." },
    "path": { "type": "string", "minLength": 1, "description": "path из session_source: сервер берёт только имя файла, чтобы сверить префикс со строками, которые прислал бинарь." },
    "source_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "source_sha256 из session_source." },
    "source_records": { "type": "integer", "minimum": 1, "description": "source_records из session_source." },
    "report": { "type": "object", "description": "Отчёт Deep v2 (deep-review.md, «Формат Deep v2»), не больше 2 МиБ JSON. Поля проверяет сервер (pkg/deepv2), ошибка — invalid с путём поля." }
  }
}`

// deepSubmitOutputSchema is the output schema of deep_submit as mcp.md gives it.
const deepSubmitOutputSchema = `{
  "type": "object",
  "required": ["candidate_id", "candidate_sha256", "status", "source_check"],
  "additionalProperties": false,
  "properties": {
    "candidate_id": { "type": "string", "description": "Id кандидата (UUID): по нему проверяющий берёт кандидата и отправляет проверку." },
    "candidate_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "sha256 канонического JSON маскированного отчёта; проверка привязывается ровно к нему." },
    "status": { "enum": ["candidate", "published"], "description": "candidate — новый или уже ждущий проверки; published — такой же отчёт уже опубликован (повтор)." },
    "source_check": { "enum": ["matched", "not_checked", "mismatch"], "description": "Справочная сверка source_sha256 со строками на сервере (deep-review.md, «Источник и L<n>»); публикацию не останавливает. not_checked — на сервере пока нет всех строк 1..source_records: историю бинарь догружает постепенно, свежие строки шлёт раз в 30 с, а некоторые не пришлёт никогда. Повтор того же отчёта возвращает хранимый итог; основание проверки — локальный deep_source_check." }
  }
}`

// deepReviewSubmitInput is the input of deep_review_submit; review is read raw (rawArgument).
type deepReviewSubmitInput struct {
	CandidateID string `json:"candidate_id"`
}

// deepReviewSubmitInputSchema is the input schema of deep_review_submit as mcp.md gives it.
const deepReviewSubmitInputSchema = `{
  "type": "object",
  "required": ["candidate_id", "review"],
  "additionalProperties": false,
  "properties": {
    "candidate_id": { "type": "string", "minLength": 1 },
    "review": {
      "type": "object",
      "description": "Объект проверки; поля проверяет ValidateSemanticReview (pkg/deepv2), ошибка — invalid с путём review.<поле>.",
      "properties": {
        "session_id": { "type": "string", "description": "Сессия кандидата." },
        "candidate_sha256": { "type": "string", "description": "candidate_sha256 из deep_candidate_get." },
        "verdict": { "type": "string", "description": "Только approved публикует." },
        "reviewer": { "type": "string", "description": "Кто проверял, свободный текст." },
        "reviewed_at": { "type": "string" },
        "note": { "type": "string" },
        "checks": {
          "type": "array",
          "items": { "enum": ["task_boundaries", "completion_claims", "all_13_checks", "proposal_choice", "sensitive_data"] },
          "description": "Все пять независимых проверок, каждая ровно один раз."
        }
      }
    }
  }
}`

// deepReviewSubmitOutputSchema is the output schema of deep_review_submit as mcp.md gives it.
const deepReviewSubmitOutputSchema = `{
  "type": "object",
  "required": ["status", "candidate_id", "candidate_sha256", "proposals_changed"],
  "additionalProperties": false,
  "properties": {
    "status": { "const": "published" },
    "candidate_id": { "type": "string" },
    "candidate_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
    "proposals_changed": { "type": "integer", "minimum": 0, "description": "Сколько предложений реестра пользователя появилось, изменилось или исчезло при пересборке." }
  }
}`

// addDeepWrite adds deep_submit and deep_review_submit: a candidate report, and the independent
// review that publishes it. A failed check answers isError with the field's path, for the
// agent to fix the report and send it again.
func addDeepWrite(server *sdkmcp.Server, deep Deep, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "deep_submit",
		Description: "Кандидат отчёта Deep v2 по сессии: сервер маскирует и проверяет его (pkg/deepv2) и хранит " +
			"как кандидата. В реестр он попадает только после независимой проверки deep_review_submit. " +
			"Ошибка проверки — invalid с путём поля: исправьте поле и отправьте снова.",
		Annotations:  &sdkmcp.ToolAnnotations{IdempotentHint: true},
		InputSchema:  json.RawMessage(deepSubmitInputSchema),
		OutputSchema: json.RawMessage(deepSubmitOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deepSubmitInput) (
		*sdkmcp.CallToolResult, DeepSubmitted, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "deep_submit: request user", "error", err)
			return nil, DeepSubmitted{}, errInternal
		}
		report, err := rawArgument(req, "report")
		if err != nil {
			logger.ErrorContext(ctx, "deep_submit: raw report", "error", err)
			return nil, DeepSubmitted{}, errInternal
		}
		if len(report) > maxReportBytes {
			return nil, DeepSubmitted{}, &ToolError{
				Code:    CodeTooLarge,
				Message: fmt.Sprintf("report: %d bytes, at most %d", len(report), maxReportBytes),
			}
		}
		got, err := deep.Submit(ctx, userID, DeepSubmission{
			SessionID: in.SessionID, Agent: in.Agent, Path: in.Path, SourceSHA256: in.SourceSHA256,
			SourceRecords: in.SourceRecords, Report: report,
		})
		if err != nil {
			return nil, DeepSubmitted{}, toolFailure(ctx, logger, "deep_submit: keep the candidate", err)
		}
		return nil, got, nil
	})

	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "deep_review_submit",
		Description: "Независимая проверка кандидата: verdict approved по всем пяти проверкам, привязанная " +
			"к точному candidate_sha256 из deep_candidate_get, публикует отчёт и пересобирает реестр.",
		Annotations:  &sdkmcp.ToolAnnotations{IdempotentHint: true},
		InputSchema:  json.RawMessage(deepReviewSubmitInputSchema),
		OutputSchema: json.RawMessage(deepReviewSubmitOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deepReviewSubmitInput) (
		*sdkmcp.CallToolResult, DeepPublished, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "deep_review_submit: request user", "error", err)
			return nil, DeepPublished{}, errInternal
		}
		candidateID, err := uuid.Parse(in.CandidateID)
		if err != nil {
			return nil, DeepPublished{}, errNoCandidate
		}
		review, err := rawArgument(req, "review")
		if err != nil {
			logger.ErrorContext(ctx, "deep_review_submit: raw review", "error", err)
			return nil, DeepPublished{}, errInternal
		}
		got, err := deep.Review(ctx, userID, candidateID, review)
		if err != nil {
			return nil, DeepPublished{}, toolFailure(ctx, logger, "deep_review_submit: publish", err)
		}
		return nil, got, nil
	})
}
