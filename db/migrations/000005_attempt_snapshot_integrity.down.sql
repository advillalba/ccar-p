DROP TRIGGER IF EXISTS answer_snapshot_option_membership ON attempt_answers;
DROP FUNCTION IF EXISTS enforce_snapshot_option_membership();
CREATE CONSTRAINT TRIGGER answer_option_membership AFTER INSERT OR UPDATE ON attempt_answers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_option_question_membership();
DROP TRIGGER IF EXISTS attempt_snapshot_version_membership ON attempts;
DROP FUNCTION IF EXISTS enforce_attempt_snapshot_version();
DROP TRIGGER IF EXISTS attempts_immutable_snapshot ON attempts;
DROP FUNCTION IF EXISTS prevent_attempt_snapshot_mutation();
