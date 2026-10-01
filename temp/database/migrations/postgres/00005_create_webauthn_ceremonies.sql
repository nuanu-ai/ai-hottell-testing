-- +goose Up
CREATE TABLE webauthn_ceremonies (
    id           uuid        NOT NULL,
    kind         text        NOT NULL,
    -- NULL for a login: the user is found by the passkey's user handle only when it finishes.
    user_id      uuid,
    -- NULL for a login; the name a registration gives the new passkey.
    passkey_name text,
    session_data jsonb       NOT NULL,
    expires_at   timestamptz NOT NULL,
    CONSTRAINT webauthn_ceremonies_pkey PRIMARY KEY (id),
    CONSTRAINT webauthn_ceremonies_kind_check CHECK (kind IN ('register', 'login')),
    CONSTRAINT webauthn_ceremonies_register_user_check CHECK (kind <> 'register' OR user_id IS NOT NULL),
    CONSTRAINT webauthn_ceremonies_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX idx_webauthn_ceremonies_expires_at ON webauthn_ceremonies (expires_at);

-- +goose Down
DROP TABLE webauthn_ceremonies;
