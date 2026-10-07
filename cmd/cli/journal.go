package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
	"git.alva.dev/alva/harness-telemetry/internal/config"
	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
)

// envJournalHead is the head of the decision journal recorded outside the database, as
// "journal verify" printed it before: "<records>:<record_hash>". Set, the journal must end at
// it, so a journal cut short or rewritten and sealed again is caught (HT-396); unset, only the
// links of the chain are checked.
const envJournalHead = "HT_JOURNAL_HEAD"

//nolint:gochecknoglobals // compiled once, never written
var headRe = regexp.MustCompile(`^(0|[1-9][0-9]*):([0-9a-f]{64})$`)

// runJournal runs the journal command on the database of pool. verify checks the whole hash
// chain of the decision journal, and its head against HT_JOURNAL_HEAD when that is set:
// "ok: N records", then "head: N:<hash>" to record outside the database, and 0; or
// "broken at seq K: why" or the head mismatch and exitFailure. A malformed HT_JOURNAL_HEAD is
// exitUsage; a failure to read the journal goes to errOut.
func runJournal(
	ctx context.Context, pool *pgxpool.Pool, command string, out, errOut io.Writer, lookup config.LookupFunc,
) int {
	if command != "verify" {
		fmt.Fprintln(errOut, "unknown command "+command)
		return exitUsage
	}
	var want *journal.Head
	if raw, ok := lookup(envJournalHead); ok && strings.TrimSpace(raw) != "" {
		m := headRe.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			fmt.Fprintln(errOut, envJournalHead+": want <records>:<record_hash>, as journal verify prints the head")
			return exitUsage
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			fmt.Fprintln(errOut, envJournalHead+": "+err.Error())
			return exitUsage
		}
		if n == 0 {
			m[2] = journal.ZeroHash
		}
		want = &journal.Head{Records: n, RecordHash: m[2]}
	}
	n, head, err := postgres.NewDecisionJournal(pool).Verify(ctx, want)
	var broken *postgres.ChainBrokenError
	switch {
	case errors.As(err, &broken):
		fmt.Fprintln(out, broken.Error())
		return exitFailure
	case errors.Is(err, journal.ErrHeadMismatch):
		fmt.Fprintln(out, err.Error())
		return exitFailure
	case err != nil:
		fmt.Fprintln(errOut, "journal verify: "+err.Error())
		return exitFailure
	}
	fmt.Fprintf(out, "ok: %d records\n", n)
	if want == nil {
		fmt.Fprintf(out, "head not checked: set %s to the head recorded before\n", envJournalHead)
	}
	fmt.Fprintf(out, "head: %d:%s\n", head.Records, head.RecordHash)
	return 0
}
