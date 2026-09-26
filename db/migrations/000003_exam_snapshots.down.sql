DROP TRIGGER IF EXISTS exams_archived_republish_guard ON exams;
DROP FUNCTION IF EXISTS prevent_archived_exam_republish();
DROP TRIGGER IF EXISTS exam_versions_immutable ON exam_versions;
DROP FUNCTION IF EXISTS prevent_exam_version_mutation();
