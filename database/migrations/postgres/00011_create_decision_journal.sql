-- +goose Up
-- The decision journal (docs/specs/deep-review/deep-review.md, «Журнал решений»): one
-- append-only journal of the server with a hash chain, holding the lifecycle events of
-- proposals and the records of the coach. Appending is serialized by the journal's
-- pg_advisory_xact_lock; the triggers below refuse any edit of the history.
CREATE TABLE decision_journal (
    -- The order of the chain.
    seq           bigint      GENERATED ALWAYS AS IDENTITY,
    record_id     uuid        NOT NULL,
    recorded_at   timestamptz NOT NULL,
    user_id       uuid        NOT NULL,
    -- What the record is about: proposal_id of a lifecycle event, topic_key of a coach record.
    subject       text        NOT NULL,
    kind          text        NOT NULL,
    -- The canonical text of the record without record_hash, byte for byte as it was hashed:
    -- record_hash is sha256(canonical). record is the same object for queries only, since
    -- jsonb reorders keys and rewrites numbers.
    canonical     text        NOT NULL,
    record        jsonb       NOT NULL,
    previous_hash char(64)    NOT NULL,
    record_hash   char(64)    NOT NULL,
    CONSTRAINT decision_journal_pkey PRIMARY KEY (seq),
    CONSTRAINT decision_journal_record_id_key UNIQUE (record_id),
    CONSTRAINT decision_journal_record_hash_key UNIQUE (record_hash),
    CONSTRAINT decision_journal_kind_check
        CHECK (kind IN ('decision', 'application', 'effect', 'coach_decision', 'coach_check')),
    CONSTRAINT decision_journal_record_check CHECK (jsonb_typeof(record) = 'object'),
    CONSTRAINT decision_journal_previous_hash_check CHECK (previous_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT decision_journal_record_hash_check CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT decision_journal_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE INDEX decision_journal_user_id_recorded_at_idx ON decision_journal (user_id, recorded_at);
CREATE INDEX decision_journal_subject_idx ON decision_journal (subject);

-- +goose StatementBegin
CREATE FUNCTION decision_journal_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'decision_journal is append-only: % refused', TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER decision_journal_no_update_delete BEFORE UPDATE OR DELETE ON decision_journal
    FOR EACH ROW EXECUTE FUNCTION decision_journal_append_only();
CREATE TRIGGER decision_journal_no_truncate BEFORE TRUNCATE ON decision_journal
    FOR EACH STATEMENT EXECUTE FUNCTION decision_journal_append_only();

-- +goose Down
DROP TABLE decision_journal;
DROP FUNCTION decision_journal_append_only();
