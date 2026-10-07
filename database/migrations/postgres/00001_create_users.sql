-- +goose Up
CREATE TABLE users (
    id               uuid        NOT NULL DEFAULT gen_random_uuid(),
    email            text        NOT NULL,
    name             text        NOT NULL,
    -- NULL until the invitation is accepted: the user is "invited" while it is NULL, "active" after.
    password_hash    text,
    webauthn_user_id bytea       NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- NULL until the first login.
    last_login_at    timestamptz,
    CONSTRAINT users_pkey PRIMARY KEY (id),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_webauthn_user_id_key UNIQUE (webauthn_user_id),
    CONSTRAINT users_email_lower_check CHECK (email = lower(email)),
    CONSTRAINT users_name_length_check CHECK (char_length(name) BETWEEN 1 AND 100)
);

-- +goose Down
DROP TABLE users;
