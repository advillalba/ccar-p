ALTER TABLE service_tokens
    ADD COLUMN content_author_id uuid REFERENCES users(id);

UPDATE service_tokens
SET content_author_id = (
    SELECT id
    FROM users
    WHERE is_active
    ORDER BY created_at, id
    LIMIT 1
)
WHERE content_author_id IS NULL;

ALTER TABLE service_tokens
    ADD CONSTRAINT service_tokens_content_author_required CHECK (content_author_id IS NOT NULL);
