CREATE FUNCTION prevent_exam_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'published exam versions are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER exam_versions_immutable
BEFORE UPDATE OR DELETE ON exam_versions
FOR EACH ROW EXECUTE FUNCTION prevent_exam_version_mutation();

CREATE FUNCTION prevent_archived_exam_republish() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'archived' AND NEW.status = 'published' THEN
        RAISE EXCEPTION 'archived exams cannot be republished' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER exams_archived_republish_guard
BEFORE UPDATE OF status ON exams
FOR EACH ROW EXECUTE FUNCTION prevent_archived_exam_republish();
