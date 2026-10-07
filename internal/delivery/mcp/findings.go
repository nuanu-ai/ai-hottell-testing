package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxFindingEvidence is how many of a finding's latest evidence the coach gets, as
// hottell-local's findings did.
const maxFindingEvidence = 3

// findingsInput is the input of findings.
type findingsInput struct {
	Since  string `json:"since"`
	Until  string `json:"until"`
	Source string `json:"source"`
	ID     string `json:"id"`
}

// findingRef is one piece of evidence of a finding.
type findingRef struct {
	Session string     `json:"session"`
	Line    int        `json:"line,omitempty"`
	At      *time.Time `json:"at,omitempty"`
	Text    string     `json:"text,omitempty"`
}

// finding is a finding of the period for the coach.
type finding struct {
	ID            string       `json:"id"`
	Source        string       `json:"source"`
	Title         string       `json:"title"`
	Kind          string       `json:"kind"`
	Pattern       string       `json:"pattern,omitempty"`
	Readiness     string       `json:"readiness,omitempty"`
	Decision      string       `json:"decision,omitempty"`
	Execution     string       `json:"execution,omitempty"`
	Effect        string       `json:"effect,omitempty"`
	Sessions      []string     `json:"sessions"`
	Evidence      []findingRef `json:"evidence"`
	EvidenceTotal int          `json:"evidence_total"`
}

// findingSources is the state of each requested source; a key is there only when the source
// was requested.
type findingSources struct {
	Reviewed string `json:"reviewed,omitempty"`
	Live     string `json:"live,omitempty"`
}

// findingsOutput is the result of findings.
type findingsOutput struct {
	Sources  findingSources `json:"sources"`
	Findings []finding      `json:"findings"`
	Undated  []finding      `json:"undated"`
}

// findingsInputSchema is the input schema of findings as mcp.md gives it.
const findingsInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "since": { "type": "string", "description": "Начало периода по времени доказательств: YYYY-MM-DD, RFC 3339 или длительность назад (168h)." },
    "until": { "type": "string", "description": "Конец периода: YYYY-MM-DD (включительно) или RFC 3339." },
    "source": { "enum": ["live", "reviewed", "all"], "description": "По умолчанию all." },
    "id": { "type": "string", "description": "Одна находка: p2:…, live:… или friction:<ключ>." }
  }
}`

// findingsOutputSchema is the output schema of findings as mcp.md gives it.
const findingsOutputSchema = `{
  "type": "object",
  "required": ["sources", "findings", "undated"],
  "additionalProperties": false,
  "properties": {
    "sources": {
      "type": "object",
      "description": "Состояние каждого запрошенного источника; ключ есть, только если источник запрошен.",
      "additionalProperties": false,
      "properties": {
        "reviewed": { "enum": ["ok", "empty"], "description": "empty — в реестре пользователя нет предложений." },
        "live": { "enum": ["ok", "empty", "missing"], "description": "Живые находки серверной аналитики (live:…) и трение (friction:<ключ>) за период. empty — их нет; missing — у сервера нет хранилища телеметрии." }
      }
    },
    "findings": { "type": "array", "items": { "$ref": "#/$defs/finding" }, "description": "Находки с доказательством со временем внутри периода; без since и until — все." },
    "undated": { "type": "array", "items": { "$ref": "#/$defs/finding" }, "description": "При заданном периоде — находки, у которых ни одно доказательство не несёт времени." }
  },
  "$defs": {
    "finding": {
      "type": "object",
      "required": ["id", "source", "title", "kind", "sessions", "evidence", "evidence_total"],
      "additionalProperties": false,
      "properties": {
        "id": { "type": "string" },
        "source": { "enum": ["reviewed", "live"] },
        "title": { "type": "string", "description": "reviewed: action предложения." },
        "kind": { "type": "string", "description": "reviewed: change_type." },
        "pattern": { "type": "string", "description": "reviewed: pattern_id." },
        "readiness": { "type": "string", "description": "Только если задано (reviewed: readiness.status)." },
        "decision": { "type": "string", "description": "Только если задано (reviewed: decision.status)." },
        "execution": { "type": "string", "description": "Только если задано (reviewed: execution.status)." },
        "effect": { "type": "string", "description": "Только если задано (reviewed: effect.status)." },
        "sessions": { "type": "array", "items": { "type": "string" } },
        "evidence": {
          "type": "array",
          "maxItems": 3,
          "description": "Последние 3 доказательства, старые первыми.",
          "items": {
            "type": "object",
            "required": ["session"],
            "additionalProperties": false,
            "properties": {
              "session": { "type": "string" },
              "line": { "type": "integer", "minimum": 1, "description": "Строка L<n> транскрипта — line событий session_read, не seq." },
              "at": { "type": "string", "format": "date-time", "description": "Время доказательства; у предложений реестра его нет." },
              "text": { "type": "string", "maxLength": 160 }
            }
          }
        },
        "evidence_total": { "type": "integer", "minimum": 0, "description": "Сколько доказательств было до обрезки до 3." }
      }
    }
  }
}`

// proposalGetInput is the input of proposal_get.
type proposalGetInput struct {
	ProposalID string `json:"proposal_id"`
}

// proposalGetInputSchema is the input schema of proposal_get as mcp.md gives it.
const proposalGetInputSchema = `{
  "type": "object",
  "required": ["proposal_id"],
  "additionalProperties": false,
  "properties": {
    "proposal_id": { "type": "string", "pattern": "^p2:[0-9a-f]{20}$" }
  }
}`

// proposalGetOutputSchema is the output schema of proposal_get as mcp.md gives it.
const proposalGetOutputSchema = `{
  "type": "object",
  "required": ["proposal_id", "group_key", "pattern_id", "change_intent", "action", "observed", "cause",
    "alternative_causes", "existing_rule", "change_type", "selection_reason", "scope", "target", "change",
    "preconditions", "exceptions", "verification", "rollback", "expected_effect", "priority", "readiness",
    "decision", "execution", "effect", "sources", "analysis_versions"],
  "additionalProperties": false,
  "properties": {
    "proposal_id": { "type": "string", "pattern": "^p2:[0-9a-f]{20}$" },
    "group_key": { "type": "string", "pattern": "^p2:[0-9a-f]{20}$" },
    "pattern_id": { "type": "string" },
    "change_intent": { "type": "string" },
    "action": { "type": "string" },
    "observed": { "type": "string" },
    "cause": { "type": "string" },
    "alternative_causes": { "type": "array", "items": { "type": "string" } },
    "existing_rule": {
      "type": "object",
      "required": ["status", "description", "locator", "version"],
      "additionalProperties": false,
      "properties": {
        "status": { "enum": ["found", "not_found", "not_checked"] },
        "description": { "type": "string" }, "locator": { "type": "string" }, "version": { "type": "string" }
      }
    },
    "change_type": { "enum": ["personalization", "project_rule", "skill", "hook", "script", "workflow", "automation", "settings", "product", "diagnosis"] },
    "selection_reason": { "type": "string" },
    "scope": { "type": "string" },
    "target": {
      "type": "object",
      "required": ["kind", "locator", "version"],
      "additionalProperties": false,
      "properties": { "kind": { "type": "string" }, "locator": { "type": "string" }, "version": { "type": "string" } }
    },
    "change": { "type": "string" },
    "preconditions": { "type": "array", "items": { "type": "string" } },
    "exceptions": { "type": "array", "items": { "type": "string" } },
    "verification": { "type": "string" },
    "rollback": { "type": "string" },
    "expected_effect": { "type": "string" },
    "priority": { "enum": ["low", "medium", "high"] },
    "readiness": {
      "type": "object", "required": ["status", "reason"], "additionalProperties": false,
      "properties": { "status": { "enum": ["hypothesis", "needs_specification", "prepared_verified"] }, "reason": { "type": "string" } }
    },
    "decision": {
      "type": "object", "required": ["status", "at"], "additionalProperties": false,
      "properties": { "status": { "enum": ["not_requested", "accepted", "rejected", "revision_requested"] }, "at": { "type": "string" } }
    },
    "execution": {
      "type": "object", "required": ["status", "at", "version", "evidence"], "additionalProperties": false,
      "properties": {
        "status": { "enum": ["not_applied", "applied"] }, "at": { "type": "string" }, "version": { "type": "string" },
        "evidence": { "type": "array", "items": { "type": "string" } }
      }
    },
    "effect": {
      "type": "object", "required": ["status", "method", "metric", "n_before", "n_after", "note"], "additionalProperties": false,
      "properties": {
        "status": { "enum": ["not_measured", "helped", "no_effect", "worse", "insufficient_data"] },
        "method": { "type": "string" }, "metric": { "type": "string" },
        "n_before": { "type": "integer", "minimum": 0 }, "n_after": { "type": "integer", "minimum": 0 },
        "note": { "type": "string" }
      }
    },
    "sources": {
      "type": "array", "minItems": 1,
      "items": {
        "type": "object", "required": ["session_id", "task_id", "source_sha256", "evidence"], "additionalProperties": false,
        "properties": {
          "session_id": { "type": "string" }, "task_id": { "type": "string" },
          "source_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
          "evidence": { "type": "array", "items": { "type": "string", "pattern": "^L[1-9][0-9]*$" } }
        }
      }
    },
    "analysis_versions": { "type": "array", "items": { "type": "string" } },
    "automation_basis": {
      "type": "object",
      "required": ["steps", "frequency", "benefit", "existing_automations_checked", "exception_handling", "permission_scope", "stop_method"],
      "additionalProperties": false,
      "properties": {
        "steps": { "type": "array", "items": { "type": "string" } },
        "frequency": { "type": "string" }, "benefit": { "type": "string" },
        "existing_automations_checked": { "type": "string" }, "exception_handling": { "type": "string" },
        "permission_scope": { "type": "string" }, "stop_method": { "type": "string" }
      }
    },
    "history_review": { "type": "string" },
    "recurrence": {
      "type": "object",
      "description": "Свёртка проверок коуча (coach_check) по этому предложению; нет — проверок не было. effect не меняет.",
      "required": ["result", "observations", "repeats", "checked_at", "checks"],
      "additionalProperties": false,
      "properties": {
        "result": { "enum": ["repeated", "not_repeated", "not_enough_data"] },
        "observations": { "type": ["integer", "null"], "minimum": 0 },
        "repeats": { "type": ["integer", "null"], "minimum": 1 },
        "checked_at": { "type": "string", "format": "date-time" },
        "checks": { "type": "integer", "minimum": 1 }
      }
    }
  }
}`

// maxLiveSessions bounds the sessions whose transcript lines one findings call resolves: each
// builds that session's timeline. Evidence of a session past it keeps its time, not its line.
const maxLiveSessions = 16

// maxEvidenceText is the longest evidence text the contract allows, in characters.
const maxEvidenceText = 160

// addLive adds the live findings of userID in per to out (only id when given) and sets
// sources.live: missing without the port or a telemetry store, empty or ok otherwise.
func addLive(ctx context.Context, live LiveFindings, userID uuid.UUID, per period, id string, out *findingsOutput) error {
	out.Sources.Live = "missing"
	if live == nil {
		return nil
	}
	found, err := live.Live(ctx, userID, per.from, per.to)
	if errors.Is(err, ErrLiveUnavailable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("live findings: %w", err)
	}
	// empty is about the period: a finding whose timed evidence all lies outside it is not there.
	out.Sources.Live = "empty"
	lines := sourceLines{live: live, user: userID, of: map[string]map[int64]int{}}
	for _, lf := range found {
		evidence, dated := liveEvidence(lf.Evidence, per)
		if per.set() && dated && len(evidence) == 0 {
			continue // every timed piece of evidence lies outside the window
		}
		out.Sources.Live = "ok"
		if id != "" && lf.ID != id {
			continue
		}
		f := finding{
			ID: lf.ID, Source: "live", Title: lf.Title, Kind: lf.Kind, Pattern: lf.Pattern,
			Readiness: lf.Readiness, Decision: lf.Decision, Execution: lf.Execution, Effect: lf.Effect,
			Sessions: append([]string{}, lf.Sessions...), Evidence: []findingRef{}, EvidenceTotal: len(evidence),
		}
		for _, e := range evidence[max(0, len(evidence)-maxFindingEvidence):] {
			ref := findingRef{Session: e.Session, Text: cut(e.Text, maxEvidenceText)}
			if !e.At.IsZero() {
				ref.At = &e.At
			}
			if n := lines.line(ctx, e.Session, e.At); n > 0 {
				ref.Line = n
			}
			f.Evidence = append(f.Evidence, ref)
		}
		if per.set() && !dated {
			out.Undated = append(out.Undated, f)
		} else {
			out.Findings = append(out.Findings, f)
		}
	}
	return nil
}

// liveEvidence is the evidence a live finding shows in per, oldest first (a piece without a
// time counts as the oldest), and whether any piece carries a time. With a period only the
// timed evidence inside it is shown; a finding with no timed evidence shows all of it.
func liveEvidence(all []LiveEvidence, per period) ([]LiveEvidence, bool) {
	dated := slices.ContainsFunc(all, func(e LiveEvidence) bool { return !e.At.IsZero() })
	var shown []LiveEvidence
	for _, e := range all {
		if !per.set() || !dated || (!e.At.IsZero() && per.contains(e.At)) {
			shown = append(shown, e)
		}
	}
	slices.SortStableFunc(shown, func(a, b LiveEvidence) int { return a.At.Compare(b.At) })
	return shown, dated
}

// contains reports whether t falls inside the period: from inclusive, to exclusive.
func (p period) contains(t time.Time) bool {
	return (p.from.IsZero() || !t.Before(p.from)) && (p.to.IsZero() || t.Before(p.to))
}

// cut shortens s to n characters.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// sourceLines resolves evidence times to transcript lines, one SourceLines call per session
// and at most maxLiveSessions sessions per findings call. Evidence without a time, of a
// session past the bound or that fails to resolve, keeps no line.
type sourceLines struct {
	live LiveFindings
	user uuid.UUID
	of   map[string]map[int64]int
}

func (s sourceLines) line(ctx context.Context, session string, at time.Time) int {
	if at.IsZero() {
		return 0
	}
	m, ok := s.of[session]
	if !ok {
		if len(s.of) >= maxLiveSessions {
			return 0
		}
		m, _ = s.live.SourceLines(ctx, s.user, session) //nolint:errcheck // a line is optional: see the type
		s.of[session] = m
	}
	return m[at.UnixMilli()]
}

// period is the window of findings over the time of the evidence; a zero end is open.
type period struct{ from, to time.Time }

func (p period) set() bool { return !p.from.IsZero() || !p.to.IsZero() }

// parseWhen reads a bound: YYYY-MM-DD, RFC 3339 or a duration back from now (168h), as
// hottell-local's ParseWhen. The server knows no zone of the user, so a date is a UTC day.
func parseWhen(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("time %q: want a date YYYY-MM-DD, RFC3339 or a duration (72h)", s)
}

// parsePeriod reads since and until; a date in until is inclusive.
func parsePeriod(since, until string, now time.Time) (period, error) {
	from, err := parseWhen(since, now)
	if err != nil {
		return period{}, fmt.Errorf("since: %w", err)
	}
	to, err := parseWhen(until, now)
	if err != nil {
		return period{}, fmt.Errorf("until: %w", err)
	}
	if len(until) == len(time.DateOnly) {
		to = to.Add(24 * time.Hour)
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return period{}, errors.New("since must be earlier than until")
	}
	return period{from: from, to: to}, nil
}

// registryProposal is the part of a registry proposal a finding shows.
type registryProposal struct {
	ProposalID string `json:"proposal_id"`
	PatternID  string `json:"pattern_id"`
	ChangeType string `json:"change_type"`
	Action     string `json:"action"`
	Readiness  struct {
		Status string `json:"status"`
	} `json:"readiness"`
	Decision struct {
		Status string `json:"status"`
	} `json:"decision"`
	Execution struct {
		Status string `json:"status"`
	} `json:"execution"`
	Effect struct {
		Status string `json:"status"`
	} `json:"effect"`
	Sources []struct {
		SessionID string   `json:"session_id"`
		Evidence  []string `json:"evidence"`
	} `json:"sources"`
}

// reviewedFinding is the finding of a registry proposal: its lines L<n> carry no time.
func reviewedFinding(doc json.RawMessage) (finding, error) {
	var p registryProposal
	if err := json.Unmarshal(doc, &p); err != nil {
		return finding{}, fmt.Errorf("decode a registry proposal: %w", err)
	}
	f := finding{
		ID: p.ProposalID, Source: "reviewed", Title: p.Action, Kind: p.ChangeType, Pattern: p.PatternID,
		Readiness: p.Readiness.Status, Decision: p.Decision.Status, Execution: p.Execution.Status,
		Effect: p.Effect.Status, Sessions: []string{}, Evidence: []findingRef{},
	}
	var refs []findingRef
	for _, s := range p.Sources {
		if !slices.Contains(f.Sessions, s.SessionID) {
			f.Sessions = append(f.Sessions, s.SessionID)
		}
		for _, l := range s.Evidence {
			if n, err := strconv.Atoi(strings.TrimPrefix(l, "L")); err == nil && n > 0 {
				refs = append(refs, findingRef{Session: s.SessionID, Line: n})
			}
		}
	}
	f.EvidenceTotal = len(refs)
	f.Evidence = append(f.Evidence, refs[max(0, len(refs)-maxFindingEvidence):]...)
	return f, nil
}

// addRegistry adds findings and proposal_get: the coach's findings of the period from the key's
// user's registry and, when live is wired, the live findings of the server analytics, and one
// registry proposal in full.
func addRegistry(server *sdkmcp.Server, registry Registry, live LiveFindings, now func() time.Time, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "findings",
		Description: "Находки периода для коуча: reviewed — предложения реестра пользователя (p2:…), live — живые находки. " +
			"Находка входит в период, только если у неё есть доказательство со временем внутри периода; " +
			"без времени — в undated. id выбирает одну находку.",
		Annotations:  &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema:  json.RawMessage(findingsInputSchema),
		OutputSchema: json.RawMessage(findingsOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in findingsInput) (
		*sdkmcp.CallToolResult, findingsOutput, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "findings: request user", "error", err)
			return nil, findingsOutput{}, errInternal
		}
		source := in.Source
		if source == "" {
			source = "all"
		}
		if source != "live" && source != "reviewed" && source != "all" {
			return nil, findingsOutput{}, &ToolError{
				Code:    CodeInvalid,
				Message: fmt.Sprintf("source: live, reviewed or all, not %q", in.Source),
			}
		}
		per, err := parsePeriod(in.Since, in.Until, now())
		if err != nil {
			return nil, findingsOutput{}, &ToolError{Code: CodeInvalid, Message: "period: " + err.Error()}
		}
		out := findingsOutput{Findings: []finding{}, Undated: []finding{}}
		if source != "reviewed" {
			if err := addLive(ctx, live, userID, per, in.ID, &out); err != nil {
				return nil, findingsOutput{}, toolFailure(ctx, logger, "findings: read the live findings", err)
			}
		}
		if source == "live" {
			return nil, out, nil
		}
		docs, err := registry.Proposals(ctx, userID)
		if err != nil {
			return nil, findingsOutput{}, toolFailure(ctx, logger, "findings: read the registry", err)
		}
		out.Sources.Reviewed = "ok"
		if len(docs) == 0 {
			out.Sources.Reviewed = "empty"
		}
		for _, doc := range docs {
			f, err := reviewedFinding(doc)
			if err != nil {
				return nil, findingsOutput{}, toolFailure(ctx, logger, "findings: read the registry", err)
			}
			if in.ID != "" && f.ID != in.ID {
				continue
			}
			// A registry line has no time: with a period nothing proves it falls inside.
			if per.set() {
				out.Undated = append(out.Undated, f)
			} else {
				out.Findings = append(out.Findings, f)
			}
		}
		return nil, out, nil
	})

	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "proposal_get",
		Description: "Полная карточка предложения реестра пользователя: четыре оси после проекции журнала " +
			"и записей коуча, recurrence — свёртка проверок коуча.",
		Annotations:  &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema:  json.RawMessage(proposalGetInputSchema),
		OutputSchema: json.RawMessage(proposalGetOutputSchema),
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in proposalGetInput) (
		*sdkmcp.CallToolResult, json.RawMessage, error,
	) {
		userID, err := requestUser(req)
		if err != nil {
			logger.ErrorContext(ctx, "proposal_get: request user", "error", err)
			return nil, nil, errInternal
		}
		doc, err := registry.Proposal(ctx, userID, in.ProposalID)
		if err != nil {
			return nil, nil, toolFailure(ctx, logger, "proposal_get: read the proposal", err)
		}
		return nil, doc, nil
	})
}
