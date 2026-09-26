CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER domains_updated_at BEFORE UPDATE ON domains FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER notes_updated_at BEFORE UPDATE ON notes FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER exams_updated_at BEFORE UPDATE ON exams FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER questions_updated_at BEFORE UPDATE ON questions FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER attempts_updated_at BEFORE UPDATE ON attempts FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE FUNCTION enforce_published_question_correctness() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_exam uuid;
    invalid_question uuid;
BEGIN
    IF TG_TABLE_NAME = 'exams' THEN
        IF TG_OP = 'DELETE' THEN
            target_exam := OLD.id;
        ELSE
            target_exam := NEW.id;
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        target_exam := OLD.exam_id;
    ELSE
        target_exam := NEW.exam_id;
    END IF;

    SELECT q.id INTO invalid_question
    FROM questions q
    JOIN exams e ON e.id = q.exam_id
    LEFT JOIN question_options o ON o.question_id = q.id
    WHERE e.id = target_exam AND e.status = 'published'
    GROUP BY q.id
    HAVING count(o.id) < 2 OR count(o.id) FILTER (WHERE o.is_correct) <> 1
    LIMIT 1;
    IF invalid_question IS NOT NULL THEN
        RAISE EXCEPTION 'published exam question % must have at least two options and exactly one correct option', invalid_question USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER published_question_correctness_exam AFTER INSERT OR UPDATE ON exams DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_published_question_correctness();
CREATE CONSTRAINT TRIGGER published_question_correctness_question AFTER INSERT OR UPDATE OR DELETE ON questions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_published_question_correctness();

CREATE FUNCTION enforce_published_option_correctness() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_exam uuid;
    invalid_question uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        SELECT q.exam_id INTO target_exam FROM questions q WHERE q.id = OLD.question_id;
    ELSE
        SELECT q.exam_id INTO target_exam FROM questions q WHERE q.id = NEW.question_id;
    END IF;

    SELECT q.id INTO invalid_question
    FROM questions q
    JOIN exams e ON e.id = q.exam_id
    LEFT JOIN question_options o ON o.question_id = q.id
    WHERE e.id = target_exam AND e.status = 'published'
    GROUP BY q.id
    HAVING count(o.id) < 2 OR count(o.id) FILTER (WHERE o.is_correct) <> 1
    LIMIT 1;
    IF invalid_question IS NOT NULL THEN
        RAISE EXCEPTION 'published exam question % must have at least two options and exactly one correct option', invalid_question USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER published_question_correctness_option AFTER INSERT OR UPDATE OR DELETE ON question_options DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_published_option_correctness();

CREATE FUNCTION enforce_option_question_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM question_options WHERE id = NEW.selected_option_id AND question_id = NEW.question_id) THEN
        RAISE EXCEPTION 'selected option does not belong to question' USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM attempts a WHERE a.id = NEW.attempt_id AND a.snapshot @> jsonb_build_object('question_ids', jsonb_build_array(NEW.question_id::text))) THEN
        RAISE EXCEPTION 'question does not belong to attempt snapshot' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER answer_option_membership AFTER INSERT OR UPDATE ON attempt_answers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_option_question_membership();

CREATE FUNCTION cleanup_expired_sessions(batch_size integer DEFAULT 500) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE
    deleted_count integer;
BEGIN
    WITH doomed AS (
        SELECT id FROM sessions WHERE expires_at < now() OR revoked_at IS NOT NULL ORDER BY expires_at LIMIT LEAST(GREATEST(batch_size, 1), 5000) FOR UPDATE SKIP LOCKED
    )
    DELETE FROM sessions s USING doomed d WHERE s.id = d.id;
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$;

CREATE INDEX notes_search_idx ON notes USING gin (to_tsvector('english', title || ' ' || summary || ' ' || markdown));
CREATE INDEX exams_search_idx ON exams USING gin (to_tsvector('english', title || ' ' || description));
