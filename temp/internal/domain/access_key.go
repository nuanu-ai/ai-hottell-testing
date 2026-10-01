package domain

import (
	"time"

	"github.com/google/uuid"
)

// AccessKeyKind is what an access key lets the hottell binary of its user do.
type AccessKeyKind string

// Access key kinds. A user has at most one active key of each kind.
const (
	// AccessKeyKindMCP authenticates the agent to the MCP server; it is shown once and
	// kept only as its hash.
	AccessKeyKindMCP AccessKeyKind = "mcp"
	// AccessKeyKindIngest is the collector token the binary sends telemetry with; its
	// open value is kept too, so every machine of the user gets the same token.
	AccessKeyKindIngest AccessKeyKind = "ingest"
)

// AccessKey is an access key of a user. Plaintext is the open value of an ingest key and
// empty for an MCP key. LastUsedAt is nil until the key is used, RevokedAt while it is
// active.
type AccessKey struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Kind       AccessKeyKind
	Plaintext  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}
