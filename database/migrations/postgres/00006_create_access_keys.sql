-- +goose Up
CREATE TABLE access_keys (
    id           uuid        NOT NULL,
    user_id      uuid        NOT NULL,
    kind         text        NOT NULL,
    key_hash     bytea       NOT NULL,
    -- The open value of an ingest token, handed to every machine of the user; NULL for an MCP key,
    -- which is shown once and kept only as its hash.
    plaintext    text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- NULL while the key has not been used.
    last_used_at timestamptz,
    -- NULL while the key is active.
    revoked_at   timestamptz,
    CONSTRAINT access_keys_pkey PRIMARY KEY (id),
    CONSTRAINT access_keys_key_hash_key UNIQUE (key_hash),
    CONSTRAINT access_keys_kind_check CHECK (kind IN ('mcp', 'ingest')),
    CONSTRAINT access_keys_plaintext_check CHECK (
        (kind = 'mcp' AND plaintext IS NULL) OR (kind = 'ingest' AND plaintext IS NOT NULL)
    ),
    CONSTRAINT access_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- At most one active key of each kind per user.
CREATE UNIQUE INDEX access_keys_user_id_kind_active_key ON access_keys (user_id, kind) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE access_keys;
