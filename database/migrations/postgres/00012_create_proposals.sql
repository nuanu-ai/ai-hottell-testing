-- +goose Up
-- The proposal registry of each user (docs/specs/deep-review/deep-review.md, «Реестр как
-- проекция»): a projection of the user's published Deep reports and the decision journal,
-- rebuilt whole for the user; wiping it and rebuilding gives the same rows.
CREATE TABLE proposals (
    user_id     uuid        NOT NULL,
    -- The proposal's group_key.
    proposal_id text        NOT NULL,
    document    jsonb       NOT NULL,
    updated_at  timestamptz NOT NULL,
    CONSTRAINT proposals_pkey PRIMARY KEY (user_id, proposal_id),
    CONSTRAINT proposals_document_check CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT proposals_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- Skill opportunity reports (deep-review.md, «Отчёт о возможностях skills»): every version a
-- user submitted; a newer one makes the current one superseded, nothing is deleted.
CREATE TABLE skill_opportunity_reports (
    id            uuid        NOT NULL,
    user_id       uuid        NOT NULL,
    document      jsonb       NOT NULL,
    -- sha256 of the report's corpus: the web compares it with the published reports of now.
    corpus_sha256 char(64)    NOT NULL,
    inventory     jsonb       NOT NULL,
    -- As the report states it: the report requires a nonempty text, not a timestamp.
    analyzed_at   text        NOT NULL,
    recorded_at   timestamptz NOT NULL,
    status        text        NOT NULL,
    CONSTRAINT skill_opportunity_reports_pkey PRIMARY KEY (id),
    CONSTRAINT skill_opportunity_reports_document_check CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT skill_opportunity_reports_inventory_check CHECK (jsonb_typeof(inventory) = 'object'),
    CONSTRAINT skill_opportunity_reports_corpus_sha256_check CHECK (corpus_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT skill_opportunity_reports_status_check CHECK (status IN ('current', 'superseded')),
    CONSTRAINT skill_opportunity_reports_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT
);

-- One current report per user.
CREATE UNIQUE INDEX skill_opportunity_reports_current_key ON skill_opportunity_reports (user_id)
    WHERE status = 'current';
CREATE INDEX skill_opportunity_reports_user_id_recorded_at_idx ON skill_opportunity_reports (user_id, recorded_at DESC);

-- +goose Down
DROP TABLE skill_opportunity_reports;
DROP TABLE proposals;
