-- +goose Up
-- A binary that cannot read its queue or its settings cache reports those counts as null
-- (docs/specs/hottell-contract/mcp.md, report_delivery_status): NULL is "unknown", not 0.
-- goose runs each direction in one transaction; the lock timeout keeps an ALTER that waits for
-- ACCESS EXCLUSIVE behind a long transaction from queueing every report upsert behind it: it
-- fails instead, and the migration is run again.
SET LOCAL lock_timeout = '5s';
ALTER TABLE delivery_status
    ALTER COLUMN queue_records DROP NOT NULL,
    ALTER COLUMN queue_bytes DROP NOT NULL,
    ALTER COLUMN settings_version DROP NOT NULL;

-- +goose Down
-- Run Down only after the service that writes NULL is stopped and the previous one deployed:
-- while the new service runs, a report with NULL committed after the UPDATE snapshot makes
-- SET NOT NULL fail and the whole Down roll back (running it again helps), and after Down the
-- new service's upsert of NULL breaks NOT NULL, so such binaries' status stays stale until the
-- previous service is deployed. No data is lost either way.
-- An unknown count goes back to the 0 it was reported as before; the next report of each
-- binary, a minute later, replaces it.
SET LOCAL lock_timeout = '5s';
UPDATE delivery_status SET queue_records = 0 WHERE queue_records IS NULL;
UPDATE delivery_status SET queue_bytes = 0 WHERE queue_bytes IS NULL;
UPDATE delivery_status SET settings_version = 0 WHERE settings_version IS NULL;
ALTER TABLE delivery_status
    ALTER COLUMN queue_records SET NOT NULL,
    ALTER COLUMN queue_bytes SET NOT NULL,
    ALTER COLUMN settings_version SET NOT NULL;
