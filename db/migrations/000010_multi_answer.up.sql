-- Multi-answer questions: attempt answers may select several options and a
-- published question must have at least one correct option (was exactly one).

ALTER TABLE attempt_answers ADD COLUMN selected_option_ids uuid[] NOT NULL DEFAULT '{}';

UPDATE attempt_answers SET selected_option_ids = ARRAY[selected_option_id];

-- Published question correctness: relax from exactly one to at least one
-- correct option. The constraint triggers from 000002 stay bound; replacing
-- the functions is enough.
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
    HAVING count(o.id) < 2 OR count(o.id) FILTER (WHERE o.is_correct) < 1
    LIMIT 1;
    IF invalid_question IS NOT NULL THEN
        RAISE EXCEPTION 'published exam question % must have at least two options and at least one correct option', invalid_question USING ERRCODE = '23514';
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
    HAVING count(o.id) < 2 OR count(o.id) FILTER (WHERE o.is_correct) < 1
    LIMIT 1;
    IF invalid_question IS NOT NULL THEN
        RAISE EXCEPTION 'published exam question % must have at least two options and at least one correct option', invalid_question USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

-- Attempt answers: every selected option must exist in the frozen attempt
-- snapshot. 000002's answer_option_membership trigger was replaced by 000005
-- with this function, so only the snapshot membership needs the array.
CREATE OR REPLACE FUNCTION enforce_snapshot_option_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM unnest(NEW.selected_option_ids) AS selected(id)
        WHERE NOT EXISTS (
            SELECT 1
            FROM attempts a, jsonb_array_elements(a.snapshot->'questions') q, jsonb_array_elements(q->'options') o
            WHERE a.id = NEW.attempt_id
              AND q->>'id' = NEW.question_id::text
              AND o->>'id' = selected.id::text
        )
    ) THEN
        RAISE EXCEPTION 'selected option does not belong to attempt snapshot question' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

-- Flush deferred constraint triggers from the backfill.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE attempt_answers
    ADD CONSTRAINT attempt_answers_selection_not_empty CHECK (array_length(selected_option_ids, 1) >= 1);

ALTER TABLE attempt_answers DROP COLUMN selected_option_id;
