package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// deepReportsUserIDFkey is the foreign key of deep_reports.user_id to users.
const deepReportsUserIDFkey = "deep_reports_user_id_fkey"

// deepReportsLockClass is the first key of the advisory lock that serializes the
// submissions of one session of a user; the second is a hash of the user and the session.
// Two-key advisory locks never collide with the one-key lock of the decision journal.
const deepReportsLockClass = 0x44454550 // "DEEP"

// deepReportColumns are the columns scanDeepReport reads, in its order.
const deepReportColumns = `id, user_id, session_id, agent, source_sha256, source_records, candidate_sha256,
	document, status, source_check, submitted_at, published_at`

// DeepReports keeps the versions of users' Deep reports and their reviews in the
// deep_reports and deep_reviews tables.
type DeepReports struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

// NewDeepReports returns a DeepReports repository on pool; it runs in the transaction of
// the context when TxManager.WithinTx opened one.
func NewDeepReports(pool *pgxpool.Pool) *DeepReports {
	return &DeepReports{pool: pool, tx: NewTxManager(pool)}
}

// InsertCandidate stores c as a candidate of its user and session, unless the user already
// has a candidate or published version of the session with the same CandidateSHA256: then
// it writes nothing and returns that version. Otherwise the user's previous candidate of the
// session becomes superseded. ID, Status, SubmittedAt and PublishedAt of c are ignored;
// domain.ErrUserNotFound when there is no such user.
func (r *DeepReports) InsertCandidate(ctx context.Context, c domain.DeepReport) (domain.DeepReport, error) {
	var stored domain.DeepReport
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		q := conn(ctx, r.pool)
		if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock($1, hashtext($2::text || ':' || $3))",
			deepReportsLockClass, c.UserID, c.SessionID); err != nil {
			return fmt.Errorf("lock deep report session: %w", err)
		}

		live, err := scanDeepReport(q.QueryRow(ctx, `SELECT `+deepReportColumns+` FROM deep_reports
			WHERE user_id = $1 AND session_id = $2 AND candidate_sha256 = $3 AND status IN ('candidate', 'published')`,
			c.UserID, c.SessionID, c.CandidateSHA256))
		if err == nil {
			stored = live
			return nil
		}
		if !errors.Is(err, domain.ErrDeepReportNotFound) {
			return err
		}

		if _, err := q.Exec(ctx, `UPDATE deep_reports SET status = 'superseded'
			WHERE user_id = $1 AND session_id = $2 AND status = 'candidate'`, c.UserID, c.SessionID); err != nil {
			return fmt.Errorf("supersede deep report candidate: %w", err)
		}
		stored, err = scanDeepReport(q.QueryRow(ctx, `
			INSERT INTO deep_reports (id, user_id, session_id, agent, source_sha256, source_records,
				candidate_sha256, document, status, source_check, submitted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'candidate', $9, now())
			RETURNING `+deepReportColumns,
			uuid.New(), c.UserID, c.SessionID, c.Agent, c.SourceSHA256, c.SourceRecords, c.CandidateSHA256,
			string(c.Document), c.SourceCheck))
		if isForeignKeyViolation(err, deepReportsUserIDFkey) {
			return domain.ErrUserNotFound
		}
		return err
	})
	if err != nil {
		return domain.DeepReport{}, err
	}
	return stored, nil
}

// Candidate returns the version with id of the user with userID, in any status;
// domain.ErrDeepReportNotFound when the user has no such version, another user's included.
func (r *DeepReports) Candidate(ctx context.Context, userID, id uuid.UUID) (domain.DeepReport, error) {
	return scanDeepReport(conn(ctx, r.pool).QueryRow(ctx,
		`SELECT `+deepReportColumns+` FROM deep_reports WHERE id = $1 AND user_id = $2`, id, userID))
}

// Publish publishes the candidate with id of the user with userID with review: the review
// is stored, the user's published version of the session becomes superseded and the
// candidate published. domain.ErrDeepReportNotFound when the user has no such version,
// domain.ErrDeepReportNotCandidate when it is not a candidate. Called within a transaction,
// it is part of it.
func (r *DeepReports) Publish(
	ctx context.Context, userID, id uuid.UUID, review domain.DeepReview,
) (domain.DeepReport, error) {
	var published domain.DeepReport
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		q := conn(ctx, r.pool)
		c, err := scanDeepReport(q.QueryRow(ctx,
			`SELECT `+deepReportColumns+` FROM deep_reports WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID))
		if err != nil {
			return err
		}
		if c.Status != domain.DeepReportCandidate {
			return domain.ErrDeepReportNotCandidate
		}

		if _, err := q.Exec(ctx, `
			INSERT INTO deep_reviews (id, report_id, user_id, review, reviewer, reviewed_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), c.ID, userID, string(review.Review), review.Reviewer, review.ReviewedAt); err != nil {
			return fmt.Errorf("insert deep review: %w", err)
		}
		if _, err := q.Exec(ctx, `UPDATE deep_reports SET status = 'superseded'
			WHERE user_id = $1 AND session_id = $2 AND status = 'published'`, userID, c.SessionID); err != nil {
			return fmt.Errorf("supersede published deep report: %w", err)
		}
		published, err = scanDeepReport(q.QueryRow(ctx, `
			UPDATE deep_reports SET status = 'published', published_at = now() WHERE id = $1
			RETURNING `+deepReportColumns, c.ID))
		return err
	})
	if err != nil {
		return domain.DeepReport{}, err
	}
	return published, nil
}

// Published returns the published version of the session with sessionID of the user with
// userID; domain.ErrDeepReportNotFound when there is none.
func (r *DeepReports) Published(ctx context.Context, userID uuid.UUID, sessionID string) (domain.DeepReport, error) {
	return scanDeepReport(conn(ctx, r.pool).QueryRow(ctx, `SELECT `+deepReportColumns+` FROM deep_reports
		WHERE user_id = $1 AND session_id = $2 AND status = 'published'`, userID, sessionID))
}

// PublishedByUser returns the published versions of every session of the user with
// userID, newest publication first.
func (r *DeepReports) PublishedByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeepReport, error) {
	return r.query(ctx, `SELECT `+deepReportColumns+` FROM deep_reports
		WHERE user_id = $1 AND status = 'published' ORDER BY published_at DESC, id`, userID)
}

// List returns the versions filter selects, newest submission first.
func (r *DeepReports) List(ctx context.Context, filter domain.DeepReportFilter) ([]domain.DeepReport, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if filter.UserID != uuid.Nil {
		add("user_id = $%d", filter.UserID)
	}
	if filter.Agent != "" {
		add("agent = $%d", filter.Agent)
	}
	if filter.Status != "" {
		add("status = $%d", filter.Status)
	}
	if !filter.From.IsZero() {
		add("submitted_at >= $%d", filter.From)
	}
	if !filter.To.IsZero() {
		add("submitted_at < $%d", filter.To)
	}
	sql := `SELECT ` + deepReportColumns + ` FROM deep_reports`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return r.query(ctx, sql+" ORDER BY submitted_at DESC, id", args...)
}

func (r *DeepReports) query(ctx context.Context, sql string, args ...any) ([]domain.DeepReport, error) {
	rows, err := conn(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("select deep reports: %w", err)
	}
	reports, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DeepReport, error) {
		return scanDeepReport(row)
	})
	if err != nil {
		return nil, fmt.Errorf("select deep reports: %w", err)
	}
	return reports, nil
}

// scanDeepReport reads the deepReportColumns of row; domain.ErrDeepReportNotFound when
// there is no row.
func scanDeepReport(row pgx.Row) (domain.DeepReport, error) {
	var (
		report   domain.DeepReport
		document string
	)
	err := row.Scan(&report.ID, &report.UserID, &report.SessionID, &report.Agent, &report.SourceSHA256,
		&report.SourceRecords, &report.CandidateSHA256, &document, &report.Status, &report.SourceCheck,
		&report.SubmittedAt, &report.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeepReport{}, domain.ErrDeepReportNotFound
	}
	if err != nil {
		return domain.DeepReport{}, fmt.Errorf("scan deep report: %w", err)
	}
	report.Document = json.RawMessage(document)
	return report, nil
}
