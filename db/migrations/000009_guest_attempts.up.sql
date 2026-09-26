ALTER TABLE attempts
    ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE attempts
    ADD COLUMN guest_token_hash bytea;

CREATE INDEX attempts_guest_idx ON attempts (guest_token_hash) WHERE guest_token_hash IS NOT NULL;

ALTER TABLE attempts
    ADD CONSTRAINT attempts_owner_check CHECK ((user_id IS NOT NULL) <> (guest_token_hash IS NOT NULL));
