package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

var errNoSourceRecords = errors.New("source prefix: records must be at least 1")

// SourcePrefix returns the sha256, in hex, of the lines 1 to records of the file key names, each
// followed by "\n", as hottell-local's session_source counts a prefix: the server's check of a
// Deep report's source_sha256 and source_records. The lines are those TranscriptLines returns,
// each once; the sum is fed as the rows are read, and the prefix is never held whole.
//
// telemetry.ErrSourceIncomplete when one of the lines is not stored. That is not always a line
// still on its way: lines the binary never sends make it permanent, and so does a file written
// with "\r\n", whose stored lines carry no "\r" and give another sum. See the port's
// SourcePrefix for the cases; a caller shows such a prefix as not checked.
func (r *Reader) SourcePrefix(ctx context.Context, key telemetry.TranscriptKey, records int) (string, error) {
	if records < 1 {
		return "", errNoSourceRecords
	}
	h := sha256.New()
	newline := []byte{'\n'}
	next := uint64(1)
	err := r.eachTranscriptLine(ctx, key, 1, uint64(records), "", "Body", func(l telemetry.TranscriptLine) error {
		// The lines come ordered by number, each once: a hole shows as a number past next.
		if l.Number != next {
			return telemetry.ErrSourceIncomplete
		}
		_, _ = io.WriteString(h, l.Body)
		_, _ = h.Write(newline)
		next++
		return nil
	})
	if errors.Is(err, telemetry.ErrSourceIncomplete) {
		return "", telemetry.ErrSourceIncomplete
	}
	if err != nil {
		return "", fmt.Errorf("source prefix: %w", err)
	}
	if next-1 < uint64(records) {
		return "", telemetry.ErrSourceIncomplete
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
