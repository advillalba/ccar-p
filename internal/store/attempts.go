package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ccar-p/study-platform/internal/attempts"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAttemptNotFound = errors.New("attempt not found")

// attemptColumns is the single source of truth for the SELECT column list
// shared by every attempt query. user_id is cast to text so guest rows
// (NULL user_id) scan cleanly into a nullable string.
const attemptColumns = `id, user_id::text, guest_token_hash, exam_id, exam_version_id, status, snapshot, correct_count, question_count, score_percentage, passed, started_at, completed_at, updated_at`

type AttemptRepository struct {
	pool  *pgxpool.Pool
	store *Store
}

func NewAttemptRepository(pool *pgxpool.Pool) *AttemptRepository {
	return &AttemptRepository{pool: pool}
}

func NewAttemptRepositoryWithStore(s *Store) *AttemptRepository {
	return &AttemptRepository{pool: s.Pool, store: s}
}

func (r *AttemptRepository) Create(ctx context.Context, owner attempts.Owner, examID, examVersionID string, snapshot exams.Snapshot) (attempts.Attempt, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return attempts.Attempt{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	attempt := attempts.Attempt{
		UserID:         owner.UserID,
		GuestTokenHash: owner.GuestTokenHash,
		ExamID:         examID,
		ExamVersionID:  examVersionID,
		Status:         attempts.StatusInProgress,
		QuestionCount:  len(snapshot.Questions),
		Snapshot:       snapshot,
	}
	runner := r.runner()
	if runner == nil {
		runner = r.pool
	}
	var userID any
	if owner.UserID != "" {
		userID = owner.UserID
	}
	var guestHash any
	if len(owner.GuestTokenHash) > 0 {
		guestHash = owner.GuestTokenHash
	}
	err = runner.QueryRow(ctx, `INSERT INTO attempts (user_id, guest_token_hash, exam_id, exam_version_id, status, snapshot, question_count) VALUES ($1, $2, $3, $4, 'in_progress', $5, $6) RETURNING id, started_at, updated_at`, userID, guestHash, examID, examVersionID, encoded, attempt.QuestionCount).Scan(&attempt.ID, &attempt.StartedAt, &attempt.UpdatedAt)
	if err != nil {
		return attempts.Attempt{}, err
	}
	return attempt, nil
}

func (r *AttemptRepository) FindByID(ctx context.Context, attemptID string, owner attempts.Owner) (attempts.Attempt, error) {
	if owner.UserID != "" {
		return scanAttempt(r.pool.QueryRow(ctx, `SELECT `+attemptColumns+` FROM attempts WHERE id = $1 AND user_id = $2`, attemptID, owner.UserID))
	}
	return scanAttempt(r.pool.QueryRow(ctx, `SELECT `+attemptColumns+` FROM attempts WHERE id = $1 AND guest_token_hash = $2`, attemptID, owner.GuestTokenHash))
}

func (r *AttemptRepository) ListByUser(ctx context.Context, userID string) ([]attempts.Attempt, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+attemptColumns+` FROM attempts WHERE user_id = $1 ORDER BY started_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []attempts.Attempt{}
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, attempt)
	}
	return items, rows.Err()
}

func (r *AttemptRepository) ListAnswers(ctx context.Context, attemptID string) (map[string]attempts.Answer, error) {
	rows, err := r.pool.Query(ctx, `SELECT attempt_id, question_id, selected_option_ids::text[], is_correct, answered_at FROM attempt_answers WHERE attempt_id = $1`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	answers := map[string]attempts.Answer{}
	for rows.Next() {
		var answer attempts.Answer
		if err := rows.Scan(&answer.AttemptID, &answer.QuestionID, &answer.SelectedOptionIDs, &answer.IsCorrect, &answer.AnsweredAt); err != nil {
			return nil, err
		}
		answers[answer.QuestionID] = answer
	}
	return answers, rows.Err()
}

func (r *AttemptRepository) UpsertAnswer(ctx context.Context, attemptID, questionID string, optionIDs []string) error {
	cmd, err := r.pool.Exec(ctx, `INSERT INTO attempt_answers (attempt_id, question_id, selected_option_ids, is_correct, answered_at)
		SELECT a.id, $2::uuid, $3::uuid[],
			(
				(SELECT count(*) FROM unnest($3::uuid[])) = (SELECT count(*) FROM jsonb_array_elements(a.snapshot->'questions') q JOIN LATERAL jsonb_array_elements(q->'options') o ON true WHERE q->>'id' = $2::text AND (o->>'is_correct')::boolean)
				AND (SELECT count(*) FROM unnest($3::uuid[]) AS s(id) WHERE EXISTS (SELECT 1 FROM jsonb_array_elements(a.snapshot->'questions') q JOIN LATERAL jsonb_array_elements(q->'options') o ON true WHERE q->>'id' = $2::text AND o->>'id' = s.id::text AND (o->>'is_correct')::boolean)) = (SELECT count(*) FROM jsonb_array_elements(a.snapshot->'questions') q JOIN LATERAL jsonb_array_elements(q->'options') o ON true WHERE q->>'id' = $2::text AND (o->>'is_correct')::boolean)
			),
			now()
		FROM attempts a
		WHERE a.id = $1::uuid AND a.status = 'in_progress'
		AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(a.snapshot->'questions') q
			WHERE q->>'id' = $2::text
		)
		AND NOT EXISTS (
			SELECT 1 FROM unnest($3::uuid[]) AS s(id) WHERE NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements(a.snapshot->'questions') q
				JOIN LATERAL jsonb_array_elements(q->'options') o ON true
				WHERE q->>'id' = $2::text AND o->>'id' = s.id::text
			)
		)
		ON CONFLICT (attempt_id, question_id) DO UPDATE SET selected_option_ids = EXCLUDED.selected_option_ids, is_correct = EXCLUDED.is_correct, answered_at = now()`, attemptID, questionID, optionIDs)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return attempts.ErrNotInProgress
	}
	return nil
}

func (r *AttemptRepository) BeginAttemptTx(ctx context.Context, attemptID string, owner attempts.Owner, fn func(ctx context.Context, tx attempts.AttemptTx) error) error {
	if r.store != nil {
		return r.store.WithinTx(ctx, func(ctx context.Context, pgxTx pgx.Tx) error {
			return fn(ctx, &pgAttemptTx{tx: pgxTx, attemptID: attemptID, owner: owner, ctx: ctx})
		})
	}
	return errors.New("store is not configured for transactions")
}

func (r *AttemptRepository) runner() DBTX {
	if r.store != nil {
		return nil
	}
	return r.pool
}

type pgAttemptTx struct {
	tx        pgx.Tx
	attemptID string
	owner     attempts.Owner
	ctx       context.Context
}

func (t *pgAttemptTx) Attempt() (attempts.Attempt, error) {
	if t.owner.UserID != "" {
		return scanAttempt(t.tx.QueryRow(t.ctx, `SELECT `+attemptColumns+` FROM attempts WHERE id = $1 AND user_id = $2 FOR UPDATE`, t.attemptID, t.owner.UserID))
	}
	return scanAttempt(t.tx.QueryRow(t.ctx, `SELECT `+attemptColumns+` FROM attempts WHERE id = $1 AND guest_token_hash = $2 FOR UPDATE`, t.attemptID, t.owner.GuestTokenHash))
}

func (t *pgAttemptTx) Answers() (map[string]attempts.Answer, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT attempt_id, question_id, selected_option_ids::text[], is_correct, answered_at FROM attempt_answers WHERE attempt_id = $1`, t.attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	answers := map[string]attempts.Answer{}
	for rows.Next() {
		var answer attempts.Answer
		if err := rows.Scan(&answer.AttemptID, &answer.QuestionID, &answer.SelectedOptionIDs, &answer.IsCorrect, &answer.AnsweredAt); err != nil {
			return nil, err
		}
		answers[answer.QuestionID] = answer
	}
	return answers, rows.Err()
}

func (t *pgAttemptTx) MarkCompleted(result attempts.Result) error {
	if _, err := t.tx.Exec(t.ctx, `UPDATE attempt_answers aa SET is_correct = (
		WITH selected AS (
			SELECT count(*) AS n FROM unnest(aa.selected_option_ids) AS s(id) WHERE EXISTS (
				SELECT 1 FROM attempts a, jsonb_array_elements(a.snapshot->'questions') q, jsonb_array_elements(q->'options') o
				WHERE a.id = aa.attempt_id AND q->>'id' = aa.question_id::text AND o->>'id' = s.id::text AND (o->>'is_correct')::boolean
			)
		), expected AS (
			SELECT count(*) AS n FROM attempts a, jsonb_array_elements(a.snapshot->'questions') q, jsonb_array_elements(q->'options') o
			WHERE a.id = aa.attempt_id AND q->>'id' = aa.question_id::text AND (o->>'is_correct')::boolean
		)
		SELECT (SELECT n FROM selected) = (SELECT n FROM expected) AND (SELECT n FROM selected) > 0
	) WHERE aa.attempt_id = $1`, t.attemptID); err != nil {
		return err
	}
	cmd, err := t.tx.Exec(t.ctx, `UPDATE attempts SET status = 'completed', correct_count = $2, score_percentage = $3, passed = $4, completed_at = now() WHERE id = $1 AND status = 'in_progress'`, t.attemptID, result.CorrectCount, result.ScorePercentage, result.Passed)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return attempts.ErrAlreadyCompleted
	}
	return nil
}

func scanAttempt(row pgx.Row) (attempts.Attempt, error) {
	var attempt attempts.Attempt
	var raw json.RawMessage
	var userID *string
	err := row.Scan(&attempt.ID, &userID, &attempt.GuestTokenHash, &attempt.ExamID, &attempt.ExamVersionID, &attempt.Status, &raw, &attempt.CorrectCount, &attempt.QuestionCount, &attempt.ScorePercent, &attempt.Passed, &attempt.StartedAt, &attempt.CompletedAt, &attempt.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return attempts.Attempt{}, ErrAttemptNotFound
		}
		return attempts.Attempt{}, err
	}
	if userID != nil {
		attempt.UserID = *userID
	}
	snapshot, err := attempts.UnmarshalSnapshot(raw)
	if err != nil {
		return attempts.Attempt{}, fmt.Errorf("unmarshal attempt snapshot: %w", err)
	}
	attempt.Snapshot = snapshot
	return attempt, nil
}
