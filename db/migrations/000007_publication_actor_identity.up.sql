ALTER TABLE publication_events
    ALTER COLUMN actor_id TYPE text USING actor_id::text;

ALTER TABLE publication_events
    ADD CONSTRAINT publication_events_actor_id_nonempty CHECK (btrim(actor_id) <> '');
