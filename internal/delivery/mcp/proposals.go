package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProposalEvent is an application or an effect of a registry proposal, written outside the
// coach's conversation; Detail is the raw JSON of its detail.
type ProposalEvent struct {
	ProposalID   string
	Event        string
	AuthorityRef string
	Detail       json.RawMessage
}

// ProposalEventRecord is the journal record of a proposal event as written.
type ProposalEventRecord struct {
	RecordID            string    `json:"record_id"`
	Kind                string    `json:"kind"`
	RecordedAt          time.Time `json:"recorded_at"`
	ProposalID          string    `json:"proposal_id"`
	ProposalFingerprint string    `json:"proposal_fingerprint"`
	RecordHash          string    `json:"record_hash"`
}

// SkillReportSubmitted is the kept skill opportunities report: the new current one, or the
// version a repeat matched.
type SkillReportSubmitted struct {
	Status       string    `json:"status"`
	ReportID     string    `json:"report_id"`
	ReportSHA256 string    `json:"report_sha256"`
	SubmittedAt  time.Time `json:"submitted_at"`
}

// proposalEventInput is the input of proposal_event; detail is read raw (rawArgument).
type proposalEventInput struct {
	ProposalID   string `json:"proposal_id"`
	Event        string `json:"event"`
	AuthorityRef string `json:"authority_ref"`
}

// proposalEventInputSchema is the input schema of proposal_event as mcp.md gives it.
const proposalEventInputSchema = `{
  "type": "object",
  "required": ["proposal_id", "event", "authority_ref", "detail"],
  "additionalProperties": false,
  "properties": {
    "proposal_id": { "type": "string", "pattern": "^p2:[0-9a-f]{20}$" },
    "event": { "enum": ["application", "effect"] },
    "authority_ref": { "type": "string", "minLength": 1, "maxLength": 1000, "description": "На что опирается событие: согласие человека в этом разговоре и т. п." },
    "detail": {
      "type": "object",
      "description": "application: ровно {version, target_version_verified, evidence, permission_ref, rule_review}; effect: ровно {status, method, metric, n_before, n_after, note, evidence}. Поля проверяет сервер, ошибка — invalid с путём detail.<поле>.",
      "properties": {
        "version": { "type": "string", "description": "application: версия цели после правки." },
        "target_version_verified": { "type": "string", "description": "application: версия цели, проверенная перед правкой; должна равняться target.version предложения." },
        "evidence": { "type": "array", "items": { "type": "string" }, "description": "Непустой массив текстов до 1000 знаков." },
        "permission_ref": { "type": "string", "description": "application: где человек разрешил правку." },
        "rule_review": { "type": "string", "description": "application: что сверено с действующими правилами." },
        "status": { "enum": ["helped", "no_effect", "worse", "insufficient_data"], "description": "effect." },
        "method": { "type": "string" },
        "metric": { "type": "string" },
        "n_before": { "type": "integer", "minimum": 0 },
        "n_after": { "type": "integer", "minimum": 0 },
        "note": { "type": "string" }
      }
    }
  }
}`

// proposalEventOutputSchema is the output schema of proposal_event as mcp.md gives it.
const proposalEventOutputSchema = `{
  "type": "object",
  "required": ["record_id", "kind", "recorded_at", "proposal_id", "proposal_fingerprint", "record_hash"],
  "additionalProperties": false,
  "properties": {
    "record_id": { "type": "string" },
    "kind": { "enum": ["application", "effect"] },
    "recorded_at": { "type": "string", "format": "date-time" },
    "proposal_id": { "type": "string" },
    "proposal_fingerprint": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Версия предложения, к которой привязано событие." },
    "record_hash": { "type": "string", "pattern": "^[0-9a-f]{64}$" }
  }
}`

// skillOpportunitiesInput is the input of skill_opportunities_submit; both objects are read raw.
type skillOpportunitiesInput struct{}

// skillOpportunitiesInputSchema is the input schema of skill_opportunities_submit as mcp.md gives it.
const skillOpportunitiesInputSchema = `{
  "type": "object",
  "required": ["report", "inventory"],
  "additionalProperties": false,
  "properties": {
    "report": { "type": "object", "description": "Отчёт skill_opportunities v1 (deep-review.md, «Отчёт о возможностях skills»); report.inventory — ровно inventory ниже." },
    "inventory": { "type": "object", "description": "Снимок из skills_inventory (hottell-local) без изменений: {snapshot_at, scope, items}." }
  }
}`

// skillOpportunitiesOutputSchema is the output schema of skill_opportunities_submit as mcp.md gives it.
const skillOpportunitiesOutputSchema = `{
  "type": "object",
  "required": ["status", "report_id", "report_sha256", "submitted_at"],
  "additionalProperties": false,
  "properties": {
    "status": { "enum": ["current", "superseded"], "description": "current — этот отчёт текущий у пользователя; superseded — идемпотентный повтор отчёта, который уже вытеснен более новым. Когда корпус меняется, веб показывает текущий отчёт устаревшим." },
    "report_id": { "type": "string" },
    "report_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "sha256 канонического JSON маскированного отчёта." },
    "submitted_at": { "type": "string", "format": "date-time" }
  }
}`

// addProposalEvent adds proposal_event: an application or an effect of a proposal of the key's
// user's registry, written to the decision journal. A decision is not written over MCP: the
// person takes it in the web or in the coach's conversation.
func addProposalEvent(server *sdkmcp.Server, journal Journal, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "proposal_event",
		Description: "Применение правки (application) или замер её эффекта (effect) по предложению реестра " +
			"пользователя: событие пишется в журнал решений, реестр пересобирается. Решение по предложению " +
			"(принять, отклонить, на доработку) через MCP не пишется: его принимает человек в вебе или в разговоре " +
			"коуча (coach_journal).",
		InputSchema:  json.RawMessage(proposalEventInputSchema),
		OutputSchema: json.RawMessage(proposalEventOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in proposalEventInput) (
		*sdkmcp.CallToolResult, ProposalEventRecord, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "proposal_event: request user", "error", err)
			return nil, ProposalEventRecord{}, errInternal
		}
		detail, err := rawArgument(req, "detail")
		if err != nil {
			logger.ErrorContext(ctx, "proposal_event: raw detail", "error", err)
			return nil, ProposalEventRecord{}, errInternal
		}
		got, err := journal.ProposalEvent(ctx, userID, ProposalEvent{
			ProposalID: in.ProposalID, Event: in.Event, AuthorityRef: in.AuthorityRef, Detail: detail,
		})
		if err != nil {
			return nil, ProposalEventRecord{}, toolFailure(ctx, logger, "proposal_event: write the event", err)
		}
		got.RecordedAt = got.RecordedAt.UTC()
		return nil, got, nil
	})
}

// addSkillOpportunities adds skill_opportunities_submit: the skill opportunities report over
// the user's published corpus, checked and kept; nothing is installed.
func addSkillOpportunities(server *sdkmcp.Server, skills SkillReports, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "skill_opportunities_submit",
		Description: "Отчёт о возможностях skills по опубликованным глубоким разборам пользователя: сервер " +
			"проверяет его и хранит текущим, но ничего не устанавливает. inventory — снимок skills_inventory " +
			"из hottell-local без изменений.",
		Annotations:  &sdkmcp.ToolAnnotations{IdempotentHint: true},
		InputSchema:  json.RawMessage(skillOpportunitiesInputSchema),
		OutputSchema: json.RawMessage(skillOpportunitiesOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, _ skillOpportunitiesInput) (
		*sdkmcp.CallToolResult, SkillReportSubmitted, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "skill_opportunities_submit: request user", "error", err)
			return nil, SkillReportSubmitted{}, errInternal
		}
		report, err := rawArgument(req, "report")
		if err != nil {
			logger.ErrorContext(ctx, "skill_opportunities_submit: raw report", "error", err)
			return nil, SkillReportSubmitted{}, errInternal
		}
		if len(report) > maxReportBytes {
			return nil, SkillReportSubmitted{}, &ToolError{
				Code:    CodeTooLarge,
				Message: fmt.Sprintf("report: %d bytes, at most %d", len(report), maxReportBytes),
			}
		}
		inventory, err := rawArgument(req, "inventory")
		if err != nil {
			logger.ErrorContext(ctx, "skill_opportunities_submit: raw inventory", "error", err)
			return nil, SkillReportSubmitted{}, errInternal
		}
		got, err := skills.Submit(ctx, userID, report, inventory)
		if err != nil {
			return nil, SkillReportSubmitted{}, toolFailure(ctx, logger, "skill_opportunities_submit: keep the report", err)
		}
		got.SubmittedAt = got.SubmittedAt.UTC()
		return nil, got, nil
	})
}
