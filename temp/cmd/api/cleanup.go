package main

import (
	"context"
	"log/slog"
	"time"
)

// cleanupInterval is how often expired sessions and WebAuthn ceremonies are removed while
// the service runs.
const cleanupInterval = 24 * time.Hour

// expiredRecords removes the records of one table that have expired.
type expiredRecords interface {
	DeleteExpired(ctx context.Context) (int, error)
}

// cleanExpired removes expired records of every table in tables, keyed by the name it
// logs, at once and then every interval until ctx is done. A failed table is logged and
// does not stop the others; the next run tries again.
func cleanExpired(ctx context.Context, tables map[string]expiredRecords, interval time.Duration, logger *slog.Logger) {
	clean := func() {
		for name, records := range tables {
			deleted, err := records.DeleteExpired(ctx)
			if err != nil {
				if ctx.Err() != nil {
					// Shutting down: the run was cut short, not failed.
					return
				}
				logger.ErrorContext(ctx, "delete expired records", "table", name, "error", err)
				continue
			}
			logger.InfoContext(ctx, "expired records deleted", "table", name, "count", deleted)
		}
	}

	clean()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}
