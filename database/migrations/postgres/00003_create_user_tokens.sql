-- +goose Up
CREATE TABLE user_tokens (
    id         uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    kind       text        NOT NULL,
    token_hash bytea       NOT NULL,
    -- NULL once the user who issued the token has been deleted.
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    -- NULL while the token has not been used.
    used_at    timestamptz,
    CONSTRAINT user_tokens_pkey PRIMARY KEY (id),
    CONSTRAINT user_tokens_token_hash_key UNIQUE (token_hash),
    CONSTRAINT user_tokens_kind_check CHECK (kind IN ('invite', 'reset')),
    CONSTRAINT user_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT user_tokens_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
);

-- At most one unused link of each kind per user.
CREATE UNIQUE INDEX user_tokens_user_id_kind_unused_key ON user_tokens (user_id, kind) WHERE used_at IS NULL;

-- +goose Down
DROP TABLE user_tokens;
