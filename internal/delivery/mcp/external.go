package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

//go:generate mockgen -destination=mock/external.go -package=mock . Keys,Users,Settings,DeliveryStatuses

// Keys recognises the MCP key a request carries and hands out the collector token.
type Keys interface {
	// Resolve returns the id of the user whose active key of kind plaintext is; a malformed
	// or unknown key, or a key of another kind, is domain.ErrAccessKeyNotFound, a revoked
	// one domain.ErrAccessKeyRevoked.
	Resolve(ctx context.Context, kind domain.AccessKeyKind, plaintext string) (uuid.UUID, error)
	// IngestToken returns the collector token of the user with userID, creating it when they
	// have none.
	IngestToken(ctx context.Context, userID uuid.UUID) (string, error)
}

// Users reads the user a request acts for.
type Users interface {
	// Me returns the user with userID; domain.ErrUserNotFound when there is none.
	Me(ctx context.Context, userID uuid.UUID) (domain.User, error)
}

// Settings reads the deny settings of a user and tells when they change.
type Settings interface {
	// Get returns the settings of the user with userID in full form and their version; the
	// defaults and version 0 when the user has never saved settings.
	Get(ctx context.Context, userID uuid.UUID) (domain.TelemetrySettings, int64, error)
	// Subscribe returns the channel the new versions of the settings of the user with userID
	// arrive on; it is closed once ctx is done.
	Subscribe(ctx context.Context, userID uuid.UUID) <-chan int64
}

// DeliveryStatuses keeps the last delivery report of each user.
type DeliveryStatuses interface {
	// Report keeps report as the delivery status of the user with userID in place of the
	// previous one and returns the status as kept, with the time the service took it.
	Report(ctx context.Context, userID uuid.UUID, report domain.DeliveryReport) (domain.DeliveryStatus, error)
}

// Deep reads the Deep reports and the registry of a user for the retro tools
// (internal/application/deep). Every method acts on the data of userID alone: a candidate of
// another user is a *ToolError not_found, as one that does not exist.
type Deep interface {
	// Context returns what an agent needs before the retro of sessionID; a session the
	// service does not know has no previous report, the rest is as for any session. An empty
	// sessionID asks for the whole published corpus and no previous report.
	Context(ctx context.Context, userID uuid.UUID, sessionID string) (DeepContext, error)
	// Candidate returns the report version with candidateID; *ToolError not_found
	// («candidate_id: no such candidate») when the user has none.
	Candidate(ctx context.Context, userID uuid.UUID, candidateID uuid.UUID) (DeepCandidate, error)
	// Submit masks, checks and keeps a candidate report of userID (deep-review.md, step 1); a
	// repeat of a kept version returns it. A failed check is *ToolError invalid with the
	// field's path.
	Submit(ctx context.Context, userID uuid.UUID, submission DeepSubmission) (DeepSubmitted, error)
	// Review checks review, the raw JSON of the independent review, against the candidate with
	// candidateID and publishes it, rebuilding the registry (steps 2–3). *ToolError
	// not_found, invalid or conflict as mcp.md «deep_review_submit» names them.
	Review(ctx context.Context, userID uuid.UUID, candidateID uuid.UUID, review json.RawMessage) (DeepPublished, error)
}

// Journal reads and appends the coach records of a user in the decision journal
// (internal/application/journal). Every method acts on the records of userID alone.
type Journal interface {
	// CoachRead returns the coach decisions of userID in journal order, each with its last
	// check folded in; the checks are not returned on their own.
	CoachRead(ctx context.Context, userID uuid.UUID, filter CoachFilter) ([]CoachEntry, error)
	// CoachAppend masks, checks and appends entry, the raw JSON of a decision or a check, and
	// returns it as written, with the id and time the journal gave it. A repeat with a
	// client_ref already written returns that record; *ToolError invalid, not_found or
	// conflict as mcp.md «coach_journal» names them.
	CoachAppend(ctx context.Context, userID uuid.UUID, entry json.RawMessage) (CoachEntry, error)
	// ProposalEvent masks, checks and appends an application or an effect of a proposal of
	// userID's registry and rebuilds the registry; an effect repeating the last one's detail
	// returns that record. *ToolError not_found or invalid as mcp.md «proposal_event» names them.
	ProposalEvent(ctx context.Context, userID uuid.UUID, event ProposalEvent) (ProposalEventRecord, error)
}

// SkillReports keeps the skill opportunities reports of a user (internal/application/deep).
type SkillReports interface {
	// Submit checks report against userID's published corpus and inventory, the raw JSON of
	// both, and keeps it as current; a repeat returns the kept version. *ToolError invalid with
	// validate_skill_report's text.
	Submit(ctx context.Context, userID uuid.UUID, report, inventory json.RawMessage) (SkillReportSubmitted, error)
}

// Registry reads the proposal registry of a user (internal/application/deep): each proposal
// is its projected document (deep-review.md «Предложение»), with recurrence when the coach
// checked it.
type Registry interface {
	// Proposals returns the proposals of userID's registry; none is an empty list.
	Proposals(ctx context.Context, userID uuid.UUID) ([]json.RawMessage, error)
	// Proposal returns the proposal proposalID of userID's registry; *ToolError not_found
	// («proposal_id: proposal id is not in the published registry») when there is none.
	Proposal(ctx context.Context, userID uuid.UUID, proposalID string) (json.RawMessage, error)
}

// ErrLiveUnavailable is LiveFindings' answer when the server has no telemetry store to build the
// analytics from: findings reports the live source missing.
var ErrLiveUnavailable = errors.New("the live findings are unavailable")

// LiveFindings is the port E2: the live findings of the server analytics
// (internal/application/analytics) of one user — the detector cards and the friction rows the
// dashboard shows, with the ids it shows (live:…, friction:<key>).
type LiveFindings interface {
	// Live returns userID's live findings over the sessions of [from, to): the longest window
	// when from is zero, up to now when to is zero; none is an empty list. Nothing after to is
	// read, so the evidence of a past window is not pushed out by later evidence (HT-515).
	// ErrLiveUnavailable without a telemetry store.
	Live(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]LiveFinding, error)
	// SourceLines maps each instant of userID's session (Unix milliseconds, the precision of
	// the analytics times) to the line L<n> of its transcript the events of that instant are
	// built from; an instant of several lines, of a subagent's transcript or of none is absent. The time, not the timeline line, ties evidence to the event: a timeline's
	// numbering depends on the window it was built over.
	SourceLines(ctx context.Context, userID uuid.UUID, session string) (map[int64]int, error)
}

// LiveFinding is one live finding: a detector card or a friction row.
type LiveFinding struct {
	ID, Title, Kind, Pattern               string
	Readiness, Decision, Execution, Effect string
	Sessions                               []string
	Evidence                               []LiveEvidence
}

// LiveEvidence is one piece of a live finding's evidence. Line is the event's line in the
// timeline of the analytics window, not in the transcript; At is zero when unknown.
type LiveEvidence struct {
	Session string
	Line    int
	At      time.Time
	Text    string
}
