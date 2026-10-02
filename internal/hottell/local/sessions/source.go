package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// SourceIn names the session whose source is frozen.
type SourceIn struct {
	Agent string `json:"agent,omitempty" jsonschema:"claude or codex; helps when the id is ambiguous"`
	ID    string `json:"id" jsonschema:"session id, a unique prefix of it, or the path of the transcript"`
}

// SourceOut is a frozen prefix of a session's transcript: what an analysis of the session
// read, so that a later look can tell whether the transcript has grown or changed since.
type SourceOut struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	SHA256    string `json:"source_sha256"`  // hex, over the bytes of the prefix
	Records   int    `json:"source_records"` // the lines of the prefix, blank ones included
	FrozenAt  string `json:"frozen_at"`      // RFC3339, UTC
}

// The codes the error of a refused source starts with.
const (
	sourceInvalid = "source_invalid"
	sourceEmpty   = "source_empty"
)

// asciiSpace is the whitespace a blank line of the source consists of.
const asciiSpace = " \t\n\v\f\r"

// Source freezes the session's transcript as it is now. The prefix is every line that ends
// in "\n" — of a .jsonl.zst, of the decompressed content — up to the first line that does
// not: a writer may still be appending that one. Its sha256 is over exactly those bytes, and
// records counts those lines the way Event.Line numbers them, blank lines included, so an
// event line n of the session is inside the prefix when n <= records. A blank line — one of
// ASCII whitespace only, "\r\n" among them — is part of the prefix and is not checked; any other line that is not JSON refuses the source with
// source_invalid, and a prefix with no JSON line in it refuses it with source_empty. The
// session is found as session_read finds it, so one Roots.Allow refuses does not exist here
// either. The file is hashed as it is read, a line at a time.
func Source(ctx context.Context, r Roots, agent, id string, now time.Time) (SourceOut, error) {
	si, err := Find(ctx, r, agent, id, now)
	if err != nil {
		return SourceOut{}, err
	}
	return sourceOf(ctx, si, now)
}

// sourceOf freezes the transcript of a session already found.
func sourceOf(ctx context.Context, si Info, now time.Time) (SourceOut, error) {
	rc, err := open(ctx, si)
	if err != nil {
		return SourceOut{}, err
	}
	defer rc.Close()
	digest := sha256.New()
	records, nonBlank, broken := 0, 0, 0
	err = eachLine(rc, rc.maxLine, func(n int, raw []byte) bool {
		if !bytes.HasSuffix(raw, []byte("\n")) {
			return false
		}
		if len(bytes.Trim(raw, asciiSpace)) > 0 {
			if !json.Valid(raw) {
				broken = n
				return false
			}
			nonBlank++
		}
		digest.Write(raw)
		records = n
		return true
	})
	if tl, ok := asTooLarge(err, si.Path); ok {
		return SourceOut{}, tl
	}
	if err != nil {
		return SourceOut{}, fmt.Errorf("source of %s: %w", si.Path, err)
	}
	if broken > 0 {
		return SourceOut{}, fmt.Errorf("%s: line %d of %s is not JSON", sourceInvalid, broken, si.Path)
	}
	if nonBlank == 0 {
		return SourceOut{}, fmt.Errorf("%s: %s has no complete record", sourceEmpty, si.Path)
	}
	return SourceOut{
		SessionID: si.ID, Agent: si.Agent, Path: si.Path,
		SHA256: hex.EncodeToString(digest.Sum(nil)), Records: records,
		FrozenAt: now.UTC().Format(time.RFC3339),
	}, nil
}
