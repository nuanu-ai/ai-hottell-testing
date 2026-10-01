-- +goose Up
CREATE TABLE passkeys (
    id               uuid        NOT NULL,
    user_id          uuid        NOT NULL,
    credential_id    bytea       NOT NULL,
    public_key       bytea       NOT NULL,
    attestation_type text        NOT NULL,
    aaguid           bytea       NOT NULL,
    sign_count       bigint      NOT NULL,
    transports       text[]      NOT NULL,
    backup_eligible  boolean     NOT NULL,
    backup_state     boolean     NOT NULL,
    name             text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- NULL until the first login with this passkey.
    last_used_at     timestamptz,
    CONSTRAINT passkeys_pkey PRIMARY KEY (id),
    CONSTRAINT passkeys_credential_id_key UNIQUE (credential_id),
    CONSTRAINT passkeys_name_length_check CHECK (char_length(name) BETWEEN 1 AND 64),
    CONSTRAINT passkeys_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX idx_passkeys_user_id ON passkeys (user_id);

-- +goose Down
DROP TABLE passkeys;
