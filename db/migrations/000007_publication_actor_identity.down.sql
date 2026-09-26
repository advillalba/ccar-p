ALTER TABLE publication_events
    DROP CONSTRAINT IF EXISTS publication_events_actor_id_nonempty,
    ALTER COLUMN actor_id TYPE uuid USING actor_id::uuid;
