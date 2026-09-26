ALTER TABLE service_tokens
    DROP CONSTRAINT IF EXISTS service_tokens_content_author_required,
    DROP COLUMN IF EXISTS content_author_id;
