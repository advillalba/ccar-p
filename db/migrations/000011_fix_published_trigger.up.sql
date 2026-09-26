-- Fix published question correctness trigger (was broken for exams table).
CREATE OR REPLACE FUNCTION enforce_published_question_correctness() RETURNS trigger LANGUAGE plpgsql AS $$
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
    HAVING count(o.id) < 2 OR count(o.id) FILTER (WHERE o.is_correct) < 1
    LIMIT 1;
    IF invalid_question IS NOT NULL THEN
        RAISE EXCEPTION 'published exam question % must have at least two options and at least one correct option', invalid_question USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;
