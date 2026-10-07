package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// skillReportsUserIDFkey is the foreign key of skill_opportunity_reports.user_id to users.
const skillReportsUserIDFkey = "skill_opportunity_reports_user_id_fkey"

// skillReportsLockClass is the first key of the advisory lock that serializes the
// submissions of one user's skill reports; the second is a hash of the user.
const skillReportsLockClass = 0x534b494c // "SKIL"

// skillReportColumns are the columns scanSkillReport reads, in its order.
const skillReportColumns = `id, user_id, document, corpus_sha256, inventory, analyzed_at, recorded_at, status`

// SkillOpportunities keeps the versions of users' skill opportunity reports in the
// skill_opportunity_reports table.
type SkillOpportunities struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

// NewSkillOpportunities returns a SkillOpportunities repository on pool; it runs in the
// transaction of the context when TxManager.WithinTx opened one.
func NewSkillOpportunities(pool *pgxpool.Pool) *SkillOpportunities {
	return &SkillOpportunities{pool: pool, tx: NewTxManager(pool)}
}

// InsertCurrent stores report as the current skill report of its user; the previous
// current one becomes superseded. A version of the user with the same document, current or
// superseded, is returned as it is and nothing is written: the check and the insert run
// under the user's lock, so parallel repeats keep one report (HT-401). ID, RecordedAt and
// Status of report are ignored; domain.ErrUserNotFound when there is no such user.
func (r *SkillOpportunities) InsertCurrent(
	ctx context.Context, report domain.SkillOpportunityReport,
) (domain.SkillOpportunityReport, error) {
	var stored domain.SkillOpportunityReport
	err := r.tx.WithinTx(ctx, func(ctx context.Context) error {
		q := conn(ctx, r.pool)
		if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock($1, hashtext($2::text))",
			skillReportsLockClass, report.UserID); err != nil {
			return fmt.Errorf("lock skill reports: %w", err)
		}
		kept, err := scanSkillReport(q.QueryRow(ctx, `SELECT `+skillReportColumns+`
			FROM skill_opportunity_reports WHERE user_id = $1 AND document = $2::jsonb
			ORDER BY recorded_at DESC, id LIMIT 1`, report.UserID, string(report.Document)))
		if err == nil {
			stored = kept
			return nil
		}
		if !errors.Is(err, domain.ErrSkillReportNotFound) {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE skill_opportunity_reports SET status = 'superseded'
			WHERE user_id = $1 AND status = 'current'`, report.UserID); err != nil {
			return fmt.Errorf("supersede skill report: %w", err)
		}
		stored, err = scanSkillReport(q.QueryRow(ctx, `
			INSERT INTO skill_opportunity_reports (id, user_id, document, corpus_sha256, inventory, analyzed_at,
				recorded_at, status)
			VALUES ($1, $2, $3::jsonb, $4, $5::jsonb, $6, now(), 'current')
			RETURNING `+skillReportColumns,
			uuid.New(), report.UserID, string(report.Document), report.CorpusSHA256, string(report.Inventory),
			report.AnalyzedAt))
		if isForeignKeyViolation(err, skillReportsUserIDFkey) {
			return domain.ErrUserNotFound
		}
		return err
	})
	if err != nil {
		return domain.SkillOpportunityReport{}, err
	}
	return stored, nil
}

// Current returns the current skill report of the user with userID;
// domain.ErrSkillReportNotFound when the user has none.
func (r *SkillOpportunities) Current(ctx context.Context, userID uuid.UUID) (domain.SkillOpportunityReport, error) {
	return scanSkillReport(conn(ctx, r.pool).QueryRow(ctx, `SELECT `+skillReportColumns+`
		FROM skill_opportunity_reports WHERE user_id = $1 AND status = 'current'`, userID))
}

// History returns every skill report version of the user with userID, newest first.
func (r *SkillOpportunities) History(ctx context.Context, userID uuid.UUID) ([]domain.SkillOpportunityReport, error) {
	rows, err := conn(ctx, r.pool).Query(ctx, `SELECT `+skillReportColumns+`
		FROM skill_opportunity_reports WHERE user_id = $1 ORDER BY recorded_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("select skill reports: %w", err)
	}
	reports, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SkillOpportunityReport, error) {
		return scanSkillReport(row)
	})
	if err != nil {
		return nil, fmt.Errorf("select skill reports: %w", err)
	}
	return reports, nil
}

// scanSkillReport reads the skillReportColumns of row; domain.ErrSkillReportNotFound when
// there is no row.
func scanSkillReport(row pgx.Row) (domain.SkillOpportunityReport, error) {
	var (
		report              domain.SkillOpportunityReport
		document, inventory string
	)
	err := row.Scan(&report.ID, &report.UserID, &document, &report.CorpusSHA256, &inventory, &report.AnalyzedAt,
		&report.RecordedAt, &report.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SkillOpportunityReport{}, domain.ErrSkillReportNotFound
	}
	if err != nil {
		return domain.SkillOpportunityReport{}, fmt.Errorf("scan skill report: %w", err)
	}
	report.Document = []byte(document)
	report.Inventory = []byte(inventory)
	return report, nil
}
