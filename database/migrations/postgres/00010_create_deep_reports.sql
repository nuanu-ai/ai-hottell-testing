-- +goose Up
-- Deep reports of sessions (docs/specs/deep-review/deep-review.md, «Протокол Deep»): every
-- version a user submitted with deep_submit. A version is never deleted; a newer one makes
-- the previous candidate or the previous published version superseded.
CREATE TABLE deep_reports (
    -- The candidate_id of the MCP tools.
    id               uuid        NOT NULL,
    user_id          uuid        NOT NULL,
    session_id       text        NOT NULL,
    agent            text        NOT NULL,
    source_sha256    char(64)    NOT NULL,
    source_records   integer     NOT NULL,
    -- sha256 of document, the canonical JSON of the masked report.
    candidate_sha256 char(64)    NOT NULL,
    -- The canonical JSON as it was hashed: jsonb would reorder keys and rewrite numbers.
    document         text        NOT NULL,
    status           text        NOT NULL,
    -- The server's informational check of source_sha256 against the stored lines;
    -- it is not part of the report and not in candidate_sha256.
    source_check     text        NOT NULL,
    submitted_at     timestamptz NOT NULL,
    -- Set when the version is published; kept when it is superseded later.
    published_at     timestamptz,
    CONSTRAINT deep_reports_pkey PRIMARY KEY (id),
    CONSTRAINT deep_reports_session_id_check CHECK (session_id ~ '^[0-9a-f-]{36}$'),
    CONSTRAINT deep_reports_agent_check CHECK (agent IN ('codex', 'claude')),
    CONSTRAINT deep_reports_source_sha256_check CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT deep_reports_source_records_check CHECK (source_records > 0),
    CONSTRAINT deep_reports_candidate_sha256_check CHECK (candidate_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT deep_reports_document_check CHECK (jsonb_typeof(document::jsonb) = 'object'),
    CONSTRAINT deep_reports_status_check CHECK (status IN ('candidate', 'published', 'superseded')),
    CONSTRAINT deep_reports_source_check_check CHECK (source_check IN ('matched', 'not_checked', 'mismatch')),
    CONSTRAINT deep_reports_published_at_check CHECK (status <> 'published' OR published_at IS NOT NULL),
    CONSTRAINT deep_reports_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT
);

-- A resubmission of the same text finds its live version instead of adding one; a match
-- with a superseded version only is an ordinary new submission.
CREATE UNIQUE INDEX deep_reports_live_version_key ON deep_reports (user_id, session_id, candidate_sha256)
    WHERE status IN ('candidate', 'published');
-- One candidate awaiting review and one published version per session of a user.
CREATE UNIQUE INDEX deep_reports_candidate_key ON deep_reports (user_id, session_id) WHERE status = 'candidate';
CREATE UNIQUE INDEX deep_reports_published_key ON deep_reports (user_id, session_id) WHERE status = 'published';
CREATE INDEX deep_reports_user_id_published_at_idx ON deep_reports (user_id, published_at DESC);

-- Independent semantic reviews: one row per review that published a candidate, including
-- the review of a candidate identical to the published version.
CREATE TABLE deep_reviews (
    id          uuid        NOT NULL,
    report_id   uuid        NOT NULL,
    -- The reviewer's user: the caller of deep_review_submit, who owns the candidate.
    user_id     uuid        NOT NULL,
    review      jsonb       NOT NULL,
    reviewer    text        NOT NULL,
    -- As the review states it: the review requires a nonempty text, not a timestamp.
    reviewed_at text        NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deep_reviews_pkey PRIMARY KEY (id),
    CONSTRAINT deep_reviews_review_check CHECK (jsonb_typeof(review) = 'object'),
    CONSTRAINT deep_reviews_report_id_fkey FOREIGN KEY (report_id) REFERENCES deep_reports (id) ON DELETE RESTRICT,
    CONSTRAINT deep_reviews_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE INDEX deep_reviews_report_id_idx ON deep_reviews (report_id);

-- +goose Down
DROP TABLE deep_reviews;
DROP TABLE deep_reports;
