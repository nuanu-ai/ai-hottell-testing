package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// hiddenTopicsUserIDFkey is the foreign key of hidden_topics.user_id to users.
const hiddenTopicsUserIDFkey = "hidden_topics_user_id_fkey"

// HiddenTopics keeps the analytics topics each user hid in the hidden_topics table.
type HiddenTopics struct {
	pool *pgxpool.Pool
}

// NewHiddenTopics returns a HiddenTopics repository on pool; it runs in the transaction of the
// context when TxManager.WithinTx opened one.
func NewHiddenTopics(pool *pgxpool.Pool) *HiddenTopics {
	return &HiddenTopics{pool: pool}
}

// Hide hides the topic key for the user with userID at at; a hidden topic stays hidden with
// its first time. domain.ErrUserNotFound when there is no such user.
func (r *HiddenTopics) Hide(ctx context.Context, userID uuid.UUID, key string, at time.Time) error {
	_, err := conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO hidden_topics (user_id, topic_key, hidden_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, topic_key) DO NOTHING`, userID, key, at)
	switch {
	case isForeignKeyViolation(err, hiddenTopicsUserIDFkey):
		return domain.ErrUserNotFound
	case err != nil:
		return fmt.Errorf("insert hidden topic: %w", err)
	}
	return nil
}

// Unhide shows the topic key to the user with userID again; a topic that was not hidden is no
// error.
func (r *HiddenTopics) Unhide(ctx context.Context, userID uuid.UUID, key string) error {
	if _, err := conn(ctx, r.pool).Exec(ctx,
		"DELETE FROM hidden_topics WHERE user_id = $1 AND topic_key = $2", userID, key); err != nil {
		return fmt.Errorf("delete hidden topic: %w", err)
	}
	return nil
}

// UnhideAll shows every topic the user with userID hid again.
func (r *HiddenTopics) UnhideAll(ctx context.Context, userID uuid.UUID) error {
	if _, err := conn(ctx, r.pool).Exec(ctx, "DELETE FROM hidden_topics WHERE user_id = $1", userID); err != nil {
		return fmt.Errorf("delete hidden topics: %w", err)
	}
	return nil
}

// List returns the topics the user with userID hid, the earliest hidden first.
func (r *HiddenTopics) List(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := conn(ctx, r.pool).Query(ctx,
		"SELECT topic_key FROM hidden_topics WHERE user_id = $1 ORDER BY hidden_at, topic_key", userID)
	if err != nil {
		return nil, fmt.Errorf("select hidden topics: %w", err)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan hidden topic: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select hidden topics: %w", err)
	}
	return keys, nil
}
