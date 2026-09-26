CREATE FUNCTION prevent_attempt_snapshot_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.snapshot IS DISTINCT FROM OLD.snapshot
       OR NEW.exam_version_id IS DISTINCT FROM OLD.exam_version_id
       OR NEW.exam_id IS DISTINCT FROM OLD.exam_id
       OR NEW.user_id IS DISTINCT FROM OLD.user_id
       OR NEW.question_count IS DISTINCT FROM OLD.question_count THEN
        RAISE EXCEPTION 'attempt identity and snapshot are immutable' USING ERRCODE = '55000';
    END IF;
    IF OLD.status = 'completed' AND ROW(NEW.status, NEW.correct_count, NEW.score_percentage, NEW.passed, NEW.completed_at)
        IS DISTINCT FROM ROW(OLD.status, OLD.correct_count, OLD.score_percentage, OLD.passed, OLD.completed_at) THEN
        RAISE EXCEPTION 'completed attempt results are immutable' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER attempts_immutable_snapshot
BEFORE UPDATE ON attempts
FOR EACH ROW EXECUTE FUNCTION prevent_attempt_snapshot_mutation();

CREATE FUNCTION enforce_attempt_snapshot_version() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    version_snapshot jsonb;
    version_exam uuid;
BEGIN
    SELECT exam_id, snapshot INTO version_exam, version_snapshot
    FROM exam_versions WHERE id = NEW.exam_version_id;
    IF version_exam IS DISTINCT FROM NEW.exam_id
       OR version_snapshot->'exam_id' IS DISTINCT FROM NEW.snapshot->'exam_id'
       OR version_snapshot->'version' IS DISTINCT FROM NEW.snapshot->'version'
       OR version_snapshot->'question_ids' IS DISTINCT FROM NEW.snapshot->'question_ids'
       OR version_snapshot->'questions' IS DISTINCT FROM NEW.snapshot->'questions' THEN
        RAISE EXCEPTION 'attempt snapshot must match its published exam version' USING ERRCODE = '23514';
    END IF;
    IF jsonb_array_length(NEW.snapshot->'questions') <> NEW.question_count THEN
        RAISE EXCEPTION 'attempt question count must match snapshot' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER attempt_snapshot_version_membership
AFTER INSERT ON attempts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_attempt_snapshot_version();

DROP TRIGGER answer_option_membership ON attempt_answers;

CREATE FUNCTION enforce_snapshot_option_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM attempts a, jsonb_array_elements(a.snapshot->'questions') q, jsonb_array_elements(q->'options') o
        WHERE a.id = NEW.attempt_id
          AND q->>'id' = NEW.question_id::text
          AND o->>'id' = NEW.selected_option_id::text
    ) THEN
        RAISE EXCEPTION 'selected option does not belong to attempt snapshot question' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER answer_snapshot_option_membership
AFTER INSERT OR UPDATE ON attempt_answers
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_snapshot_option_membership();
