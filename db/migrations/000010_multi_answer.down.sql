-- Restore single-answer attempt_answers and exactly-one published correctness.

ALTER TABLE attempt_answers ADD COLUMN selected_option_id uuid;

UPDATE attempt_answers
SET selected_option_id = (
    SELECT option
    FROM unnest(selected_option_ids) WITH ORDINALITY AS t(option, ord)
    ORDER BY t.ord
    LIMIT 1
);

ALTER TABLE attempt_answers ALTER COLUMN selected_option_id SET NOT NULL;

ALTER TABLE attempt_answers DROP CONSTRAINT attempt_answers_selection_not_empty;
ALTER TABLE attempt_answers DROP COLUMN selected_option_ids;

CREATE OR REPLACE FUNCTION enforce_published_question_correctness() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_exam uuid;
    invalid_question uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
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

CREATE OR REPLACE FUNCTION enforce_published_option_correctness() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION enforce_snapshot_option_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM attempts a, jsonb_array_elements(a.snapshot->'questions') q, jsonb_array_elements(q->'options') o
        WHERE a.id = NEW.attempt_id
          AND q->>'id' = NEW.question_id::text
          AND o->>'id' = (SELECT option FROM unnest(NEW.selected_option_ids) WITH ORDINALITY AS t(option, ord) ORDER BY t.ord LIMIT 1)::text
    ) THEN
        RAISE EXCEPTION 'selected option does not belong to attempt snapshot question' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
