package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PracticeAnswer struct {
	QuestionID        string    `json:"question_id"`
	SelectedOptionIDs []string  `json:"selected_option_ids"`
	IsCorrect         bool      `json:"is_correct"`
	AnsweredAt        time.Time `json:"answered_at"`
}

type PracticeRepository struct {
	pool *pgxpool.Pool
}

func NewPracticeRepository(pool *pgxpool.Pool) *PracticeRepository {
	return &PracticeRepository{pool: pool}
}

func NewPracticeRepositoryWithStore(s *Store) *PracticeRepository {
	return &PracticeRepository{pool: s.Pool}
}

func (r *PracticeRepository) UpsertAnswer(ctx context.Context, userID, questionID string, selected []string, isCorrect bool) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO practice_answers (user_id, question_id, selected_option_ids, is_correct, answered_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid[], $4, now())
		ON CONFLICT (user_id, question_id) DO UPDATE SET selected_option_ids = EXCLUDED.selected_option_ids, is_correct = EXCLUDED.is_correct, answered_at = now()`, userID, questionID, selected, isCorrect)
	return err
}

func (r *PracticeRepository) ListAnswers(ctx context.Context, userID string) (map[string]PracticeAnswer, error) {
	rows, err := r.pool.Query(ctx, `SELECT question_id::text, selected_option_ids::text[], is_correct, answered_at FROM practice_answers WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PracticeAnswer{}
	for rows.Next() {
		var a PracticeAnswer
		if err := rows.Scan(&a.QuestionID, &a.SelectedOptionIDs, &a.IsCorrect, &a.AnsweredAt); err != nil {
			return nil, err
		}
		out[a.QuestionID] = a
	}
	return out, rows.Err()
}

func (r *PracticeRepository) DeleteAll(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM practice_answers WHERE user_id = $1::uuid`, userID)
	return err
}
