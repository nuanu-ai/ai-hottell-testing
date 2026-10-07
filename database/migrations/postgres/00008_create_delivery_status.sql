-- +goose Up
-- The last delivery status the hottell binary of each user reported with
-- report_delivery_status (docs/specs/hottell-contract/mcp.md); a report replaces the row.
CREATE TABLE delivery_status (
    user_id          uuid        NOT NULL,
    queue_records    bigint      NOT NULL,
    queue_bytes      bigint      NOT NULL,
    -- NULL before the service first took a request of the binary.
    last_success_at  timestamptz,
    -- '' after a success; the service cuts a longer error to 500 characters.
    last_error       text        NOT NULL,
    -- When last_error happened, as the binary reported it; NULL when it did not report one.
    last_error_at    timestamptz,
    unauthorized     boolean     NOT NULL,
    settings_version bigint      NOT NULL,
    binary_version   text        NOT NULL,
    -- When the service took the report, by its own clock.
    reported_at      timestamptz NOT NULL,
    CONSTRAINT delivery_status_pkey PRIMARY KEY (user_id),
    CONSTRAINT delivery_status_queue_records_check CHECK (queue_records >= 0),
    CONSTRAINT delivery_status_queue_bytes_check CHECK (queue_bytes >= 0),
    CONSTRAINT delivery_status_last_error_check CHECK (char_length(last_error) <= 500),
    CONSTRAINT delivery_status_settings_version_check CHECK (settings_version >= 0),
    CONSTRAINT delivery_status_binary_version_check CHECK (char_length(binary_version) <= 100),
    CONSTRAINT delivery_status_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- +goose Down
-- The status is rebuilt by the next report of each binary, a minute later.
DROP TABLE delivery_status;
