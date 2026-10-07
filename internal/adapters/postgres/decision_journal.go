package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain/journal"
)

// decisionJournalLock is the one-key advisory lock that serializes appends to the decision
// journal: the colleague's fcntl.flock around record_event. One-key locks never collide with
// the two-key locks of the Deep reports.
const decisionJournalLock = 0x4A524E4C // "JRNL"

// DecisionJournal is the server's append-only decision journal in the decision_journal table:
// the lifecycle events of proposals and the coach records, chained by record_hash.
type DecisionJournal struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

// NewDecisionJournal returns a DecisionJournal on pool; it runs in the transaction of the
// context when TxManager.WithinTx opened one.
func NewDecisionJournal(pool *pgxpool.Pool) *DecisionJournal {
	return &DecisionJournal{pool: pool, tx: NewTxManager(pool)}
}

// Append adds one record at the end of the journal. Under the journal's lock it reads the
// record_hash of the last record (journal.ZeroHash for an empty journal) and hands it to
// build, which prepares the record — reading the journal with the ctx it gets sees the same
// transaction; then the record is sealed onto that hash, checked and stored with its
// canonical text byte for byte. An error of build, of sealing or of the check writes nothing.
// Called within a transaction, it is part of it, and the lock is held until it ends.
func (j *DecisionJournal) Append(
	ctx context.Context, build func(ctx context.Context, previousHash string) (journal.Record, error),
) (journal.Record, error) {
	var sealed journal.Record
	err := j.tx.WithinTx(ctx, func(ctx context.Context) error {
		q := conn(ctx, j.pool)
		if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(decisionJournalLock)); err != nil {
			return fmt.Errorf("lock decision journal: %w", err)
		}
		prev := journal.ZeroHash
		err := q.QueryRow(ctx, "SELECT record_hash FROM decision_journal ORDER BY seq DESC LIMIT 1").Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read last record hash: %w", err)
		}
		r, err := build(ctx, prev)
		if err != nil {
			return err
		}
		if sealed, err = r.Seal(prev); err != nil {
			return err
		}
		return j.insert(ctx, q, sealed)
	})
	if err != nil {
		return journal.Record{}, err
	}
	return sealed, nil
}

func (j *DecisionJournal) insert(ctx context.Context, q querier, r journal.Record) error {
	var subject string
	switch {
	case r.IsLifecycle():
		if err := journal.ValidateRecord(r, r.PreviousHash); err != nil {
			return err
		}
		subject = r.ProposalID
	case r.IsCoach() && r.Coach != nil:
		subject = r.Coach.TopicKey
	default:
		return fmt.Errorf("record %s: invalid lifecycle event or broken hash chain", r.RecordID)
	}
	recordID, err := uuid.Parse(r.RecordID)
	if err != nil {
		return fmt.Errorf("record_id: %w", err)
	}
	userID, err := uuid.Parse(r.UserID)
	if err != nil {
		return fmt.Errorf("user_id: %w", err)
	}
	at, err := time.Parse(time.RFC3339Nano, r.RecordedAt)
	if err != nil {
		return fmt.Errorf("recorded_at: %w", err)
	}
	text, err := r.CanonicalText()
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO decision_journal (record_id, recorded_at, user_id, subject, kind, canonical, record,
			previous_hash, record_hash)
		VALUES ($1, $2, $3, $4, $5, $6::text, $6::text::jsonb, $7, $8)`,
		recordID, at, userID, subject, r.Kind, string(text), r.PreviousHash, r.RecordHash); err != nil {
		return fmt.Errorf("insert decision journal record: %w", err)
	}
	return nil
}

// Lock takes, within the transaction of ctx, the journal's advisory lock that Append takes,
// and holds it to the transaction's end: what is read under it misses no record appended
// meanwhile. Outside a transaction the lock would end with the statement, so it is an error.
func (j *DecisionJournal) Lock(ctx context.Context) error {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	if !ok {
		return errors.New("lock decision journal: no transaction in the context")
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(decisionJournalLock)); err != nil {
		return fmt.Errorf("lock decision journal: %w", err)
	}
	return nil
}

// Records returns every record of the user with userID, in chain order.
func (j *DecisionJournal) Records(ctx context.Context, userID uuid.UUID) ([]journal.Record, error) {
	return j.query(ctx, "SELECT canonical, record_hash FROM decision_journal WHERE user_id = $1 ORDER BY seq", userID)
}

// All returns the whole journal in chain order.
func (j *DecisionJournal) All(ctx context.Context) ([]journal.Record, error) {
	return j.query(ctx, "SELECT canonical, record_hash FROM decision_journal ORDER BY seq")
}

// BySubject returns the records about subject — the events of a proposal_id, the coach
// records of a topic_key — in chain order.
func (j *DecisionJournal) BySubject(ctx context.Context, subject string) ([]journal.Record, error) {
	return j.query(ctx, "SELECT canonical, record_hash FROM decision_journal WHERE subject = $1 ORDER BY seq", subject)
}

// ByUser returns the records of the user with userID recorded in [from, to), in chain order.
func (j *DecisionJournal) ByUser(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]journal.Record, error) {
	return j.query(ctx, `SELECT canonical, record_hash FROM decision_journal
		WHERE user_id = $1 AND recorded_at >= $2 AND recorded_at < $3 ORDER BY seq`, userID, from, to)
}

func (j *DecisionJournal) query(ctx context.Context, sql string, args ...any) ([]journal.Record, error) {
	rows, err := conn(ctx, j.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query decision journal: %w", err)
	}
	defer rows.Close()
	var out []journal.Record
	for rows.Next() {
		var text, hash string
		if err := rows.Scan(&text, &hash); err != nil {
			return nil, fmt.Errorf("scan decision journal: %w", err)
		}
		r, err := journal.ParseRecord([]byte(text), hash)
		if err != nil {
			return nil, fmt.Errorf("decision journal record: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read decision journal: %w", err)
	}
	return out, nil
}

// ChainBrokenError is why Verify refused the journal: the seq of the first row that does not
// hold, and why.
type ChainBrokenError struct {
	Seq int64
	Err error
}

func (e *ChainBrokenError) Error() string { return fmt.Sprintf("broken at seq %d: %v", e.Seq, e.Err) }

func (e *ChainBrokenError) Unwrap() error { return e.Err }

// Verify reads the whole journal in chain order and checks it with journal.VerifyChain: it
// returns how many records hold, or a *ChainBrokenError naming the first row that does not —
// one that does not parse, or one VerifyChain refuses.
//
// want is the head recorded outside the table (HT-396); the journal must end at it, or Verify
// returns an error that is journal.ErrHeadMismatch. nil checks the links only: then a journal
// cut short, or rewritten and sealed again, is not caught. head is the journal's own head, for
// the caller to record outside the table.
func (j *DecisionJournal) Verify(ctx context.Context, want *journal.Head) (int, journal.Head, error) {
	rows, err := conn(ctx, j.pool).Query(ctx, "SELECT seq, canonical, record_hash FROM decision_journal ORDER BY seq")
	if err != nil {
		return 0, journal.Head{}, fmt.Errorf("query decision journal: %w", err)
	}
	defer rows.Close()
	var (
		seqs []int64
		recs []journal.Record
	)
	for rows.Next() {
		var (
			seq        int64
			text, hash string
		)
		if err := rows.Scan(&seq, &text, &hash); err != nil {
			return 0, journal.Head{}, fmt.Errorf("scan decision journal: %w", err)
		}
		r, err := journal.ParseRecord([]byte(text), hash)
		if err != nil {
			return 0, journal.Head{}, &ChainBrokenError{Seq: seq, Err: err}
		}
		seqs = append(seqs, seq)
		recs = append(recs, r)
	}
	if err := rows.Err(); err != nil {
		return 0, journal.Head{}, fmt.Errorf("read decision journal: %w", err)
	}
	head := journal.HeadOf(recs)
	if want == nil {
		want = &head
	}
	if err := journal.VerifyChain(recs, *want); err != nil {
		var ce *journal.ChainError
		if errors.As(err, &ce) {
			return 0, journal.Head{}, &ChainBrokenError{Seq: seqs[ce.Index], Err: ce.Err}
		}
		return 0, head, err
	}
	return len(recs), head, nil
}
