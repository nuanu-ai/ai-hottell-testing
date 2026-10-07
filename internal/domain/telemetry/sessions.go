package telemetry

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNoSessionPeriod is answered by a read of sessions whose filter has no period: From and To
// are both required and To must be after From, so that a read never scans the whole store.
var ErrNoSessionPeriod = errors.New("sessions need a period: from and to, to after from")

// SessionSummary is one session of a person as the hooks and the transcripts of a period show
// it.
type SessionSummary struct {
	// UserID is the person; uuid.Nil for hook records the server could not attribute.
	UserID uuid.UUID
	// Agent is "claude" or "codex".
	Agent     string
	SessionID string
	// Cwd is the first non-empty working directory the hooks sent; empty when the session has
	// no hooks in the period.
	Cwd string
	// FirstAt and LastAt are the times of the first and the last record of the session in the
	// period, hook or transcript line.
	FirstAt, LastAt time.Time
	// HasHooks and HasTranscript tell whether the period holds hook events and transcript lines
	// of the session: a person may turn either source off.
	HasHooks, HasTranscript bool
}

// SkillSnapshot is the list of the skills installed for an agent when a session started, as the
// binary sent it in a hottell-skills record. It lists files on disk: it does not prove that the
// agent offered or used a skill, and the skills of plugins are not in it.
type SkillSnapshot struct {
	CapturedAt time.Time `json:"captured_at"`
	Source     string    `json:"source"`
	// Complete is false while the skills of plugins are not enumerated.
	Complete bool `json:"complete"`
	// LimitHit is set when a file or the snapshot was over its limit and skills were left out.
	LimitHit bool        `json:"limit_hit"`
	Items    []SkillItem `json:"items"`
}

// SessionSkillSnapshot is the latest skill snapshot of one session of a person and agent.
type SessionSkillSnapshot struct {
	UserID    uuid.UUID
	Agent     string
	SessionID string
	Snapshot  SkillSnapshot
	// Err is set, and Snapshot empty, when the stored snapshot does not decode; it names where
	// without the stored text, and is the error of the session's own read.
	Err error
}

// SkillItem is one installed skill of a SkillSnapshot.
type SkillItem struct {
	Name string `json:"name"`
	// Description is empty when the skill's header has none.
	Description string    `json:"description,omitempty"`
	SHA256      string    `json:"sha256"`
	ModifiedAt  time.Time `json:"modified_at"`
	// PathClass is the skill's directory relative to its root, such as ~/.claude/skills/<dir>.
	PathClass string `json:"path_class"`
}
