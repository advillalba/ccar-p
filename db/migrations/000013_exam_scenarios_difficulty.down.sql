-- difficulty_level enum values cannot be removed without recreating the type.
-- Down migration is a no-op; recreate the type without 'exam_scenarios' if rollback is required.
SELECT 1;
