-- +goose Up
CREATE TABLE telemetry_settings (
    user_id    uuid        NOT NULL,
    -- The deny settings of docs/specs/hottell-contract/settings.schema.json without the version,
    -- which lives in its own column.
    document   jsonb       NOT NULL,
    -- Grows by one with every save; a user without a row has version 0 and nothing denied.
    version    bigint      NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT telemetry_settings_pkey PRIMARY KEY (user_id),
    CONSTRAINT telemetry_settings_document_check CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT telemetry_settings_version_check CHECK (version > 0),
    CONSTRAINT telemetry_settings_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE telemetry_settings;
