ALTER TABLE attempts
    DROP CONSTRAINT IF EXISTS attempts_owner_check;

DROP INDEX IF EXISTS attempts_guest_idx;

ALTER TABLE attempts
    DROP COLUMN IF EXISTS guest_token_hash;

DELETE FROM attempts WHERE user_id IS NULL;

ALTER TABLE attempts
    ALTER COLUMN user_id SET NOT NULL;
