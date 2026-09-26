ALTER TABLE service_tokens
    ADD CONSTRAINT service_tokens_name_nonempty CHECK (btrim(name) <> ''),
    ADD CONSTRAINT service_tokens_hash_sha256 CHECK (octet_length(token_hash) = 32),
    ADD CONSTRAINT service_tokens_scopes_nonempty CHECK (cardinality(scopes) > 0),
    ADD CONSTRAINT service_tokens_scopes_known CHECK (
        scopes <@ ARRAY[
            'notes:read', 'notes:write', 'notes:publish',
            'exams:read', 'exams:write', 'exams:publish',
            'epub:export', 'audit:read'
        ]::text[]
    ),
    ADD CONSTRAINT service_tokens_expiry_after_creation CHECK (expires_at IS NULL OR expires_at > created_at);

CREATE INDEX service_tokens_active_hash_idx
    ON service_tokens(token_hash)
    WHERE revoked_at IS NULL;

ALTER TABLE mcp_audit_events
    ALTER COLUMN service_token_id DROP NOT NULL,
    ADD COLUMN actor_id text NOT NULL DEFAULT '',
    ADD COLUMN request_id text NOT NULL DEFAULT '',
    ADD COLUMN error_code text NOT NULL DEFAULT '',
    ADD COLUMN duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    ADD CONSTRAINT mcp_audit_actor_nonempty CHECK (btrim(actor_id) <> '');

CREATE INDEX mcp_audit_token_created_idx
    ON mcp_audit_events(service_token_id, created_at DESC);
CREATE INDEX mcp_audit_tool_created_idx
    ON mcp_audit_events(tool, created_at DESC);
