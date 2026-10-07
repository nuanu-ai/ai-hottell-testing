-- +goose Up
-- The analytics topics each user marked «not a problem»: a topic is hidden for that user only,
-- the others still see it. topic_key is the stable id of the card.
CREATE TABLE hidden_topics (
    user_id   uuid        NOT NULL,
    topic_key text        NOT NULL,
    -- When the user first hid the topic; hiding it again keeps this time.
    hidden_at timestamptz NOT NULL,
    CONSTRAINT hidden_topics_pkey PRIMARY KEY (user_id, topic_key),
    CONSTRAINT hidden_topics_topic_key_check CHECK (char_length(topic_key) BETWEEN 1 AND 200),
    CONSTRAINT hidden_topics_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- +goose Down
-- The users hide their topics again; nothing else reads the table.
DROP TABLE hidden_topics;
