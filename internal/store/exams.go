package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/jackc/pgx/v5"
)

type ExamRepository struct{ store *Store }

type QuestionRepository struct{ store *Store }

type OptionRepository struct{ store *Store }

type QuestionReferenceRepository struct{ store *Store }

func NewExamRepository(store *Store) *ExamRepository { return &ExamRepository{store: store} }
func NewQuestionRepository(store *Store) *QuestionRepository {
	return &QuestionRepository{store: store}
}
func NewOptionRepository(store *Store) *OptionRepository { return &OptionRepository{store: store} }
func NewQuestionReferenceRepository(store *Store) *QuestionReferenceRepository {
	return &QuestionReferenceRepository{store: store}
}

var ErrPublishedExamNotFound = errors.New("published exam not found")
var ErrExamUnavailableForAttempt = errors.New("exam is not available for attempts")
var ErrArchivedQuestionCannotPatch = errors.New("archived question cannot be patched")
var ErrQuestionNotFound = errors.New("question not found")
var ErrQuestionNotFoundInSnapshot = errors.New("question not found in snapshot")

func (r *ExamRepository) Create(ctx context.Context, exam exams.Exam) (exams.Exam, error) {
	if strings.TrimSpace(exam.AuthorID) == "" {
		return exams.Exam{}, exams.FieldErrors{"author_id": "is required"}
	}
	if err := exam.ValidateInput(); err != nil {
		return exams.Exam{}, err
	}
	err := r.store.Pool.QueryRow(ctx, `INSERT INTO exams (author_id,title,slug,description,difficulty,time_limit_minutes,pass_percentage) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id,status,version,created_at,updated_at`, exam.AuthorID, strings.TrimSpace(exam.Title), exam.Slug, strings.TrimSpace(exam.Description), exam.Difficulty, exam.TimeLimitMinutes, exam.PassPercentage).Scan(&exam.ID, &exam.Status, &exam.Version, new(time.Time), new(time.Time))
	return exam, err
}

func (r *ExamRepository) Update(ctx context.Context, exam exams.Exam) error {
	if strings.TrimSpace(exam.ID) == "" {
		return exams.FieldErrors{"id": "is required"}
	}
	if strings.TrimSpace(exam.AuthorID) == "" {
		return exams.FieldErrors{"author_id": "is required"}
	}
	if err := exam.ValidateInput(); err != nil {
		return err
	}
	cmd, err := r.store.Pool.Exec(ctx, `UPDATE exams SET title=$2,slug=$3,description=$4,difficulty=$5,time_limit_minutes=$6,pass_percentage=$7 WHERE id=$1 AND author_id=$8 AND status='draft'`, exam.ID, strings.TrimSpace(exam.Title), exam.Slug, strings.TrimSpace(exam.Description), exam.Difficulty, exam.TimeLimitMinutes, exam.PassPercentage, exam.AuthorID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft exam not found")
	}
	return nil
}

func (r *ExamRepository) Delete(ctx context.Context, id string) error {
	cmd, err := r.store.Pool.Exec(ctx, `DELETE FROM exams WHERE id=$1 AND status='draft'`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft exam not found")
	}
	return nil
}

func (r *ExamRepository) Load(ctx context.Context, id string) (exams.Exam, error) {
	var exam exams.Exam
	err := r.store.Pool.QueryRow(ctx, `SELECT id,author_id,title,slug,description,difficulty,time_limit_minutes,pass_percentage,status,version,published_at FROM exams WHERE id=$1`, id).Scan(&exam.ID, &exam.AuthorID, &exam.Title, &exam.Slug, &exam.Description, &exam.Difficulty, &exam.TimeLimitMinutes, &exam.PassPercentage, &exam.Status, &exam.Version, &exam.PublishedAt)
	if err != nil {
		return exams.Exam{}, err
	}
	questions, err := loadQuestions(ctx, r.store.Pool, exam.ID)
	if err != nil {
		return exams.Exam{}, err
	}
	exam.Questions = questions
	return exam, nil
}

func (r *ExamRepository) Publish(ctx context.Context, id string) (exams.Snapshot, error) {
	exam, err := r.Load(ctx, id)
	if err != nil {
		return exams.Snapshot{}, err
	}
	return r.PublishVersion(ctx, id, exam.AuthorID, exam.Version+1)
}

func (r *ExamRepository) PublishVersion(ctx context.Context, id, _ string, version int) (exams.Snapshot, error) {
	var snapshot exams.Snapshot
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		exam, err := loadExamForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if exam.Status == exams.StatusArchived {
			return exams.ErrArchivedCannotPublish
		}
		if exam.Status != exams.StatusDraft {
			return fmt.Errorf("cannot publish exam in %s state", exam.Status)
		}
		if version != exam.Version+1 {
			return errors.New("stale prospective exam version")
		}
		if validation := exams.ValidateForPublication(exam); len(validation) != 0 {
			return exams.ValidationErrors(validation)
		}
		snapshot, err = exams.BuildSnapshot(exam, version)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("marshal exam snapshot: %w", err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO exam_versions (exam_id,version,snapshot) VALUES ($1,$2,$3) RETURNING id`, id, snapshot.Version, encoded).Scan(new(string)); err != nil {
			return err
		}
		cmd, err := tx.Exec(ctx, `UPDATE exams SET status='published', version=$2, published_at=now() WHERE id=$1 AND status='draft'`, id, snapshot.Version)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() != 1 {
			return errors.New("exam state changed during publication")
		}
		return nil
	})
	return snapshot, err
}

func (r *ExamRepository) Archive(ctx context.Context, id string) error {
	_, err := r.ArchiveVersion(ctx, id, "")
	return err
}

func (r *ExamRepository) ArchiveVersion(ctx context.Context, id, _ string) (exams.Snapshot, error) {
	var snapshot exams.Snapshot
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		exam, err := loadExamForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if exam.Status != exams.StatusDraft && exam.Status != exams.StatusPublished {
			return errors.New("exam cannot be archived")
		}
		cmd, err := tx.Exec(ctx, `UPDATE exams SET status='archived' WHERE id=$1 AND status=$2`, id, exam.Status)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() != 1 {
			return errors.New("exam state changed during archival")
		}
		snapshot = exams.Snapshot{ExamID: exam.ID, Version: exam.Version, Title: exam.Title, Slug: exam.Slug}
		return nil
	})
	return snapshot, err
}

func (r *ExamRepository) CurrentSnapshot(ctx context.Context, id string) (exams.Snapshot, string, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var status exams.Status
	var version int
	err := r.store.Pool.QueryRow(ctx, `SELECT status, version FROM exams WHERE id = $1`, id).Scan(&status, &version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return exams.Snapshot{}, "", ErrExamUnavailableForAttempt
		}
		return exams.Snapshot{}, "", err
	}
	if status != exams.StatusPublished {
		return exams.Snapshot{}, "", ErrExamUnavailableForAttempt
	}
	var raw json.RawMessage
	var versionID string
	err = r.store.Pool.QueryRow(ctx, `SELECT id, snapshot FROM exam_versions WHERE exam_id = $1 AND version = $2`, id, version).Scan(&versionID, &raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return exams.Snapshot{}, "", ErrExamUnavailableForAttempt
		}
		return exams.Snapshot{}, "", err
	}
	var snapshot exams.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return exams.Snapshot{}, "", fmt.Errorf("decode exam snapshot: %w", err)
	}
	return snapshot, versionID, nil
}

func (r *QuestionRepository) Create(ctx context.Context, question exams.Question) (exams.Question, error) {
	if err := question.ValidateInput(); err != nil {
		return exams.Question{}, err
	}
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireDraftExam(ctx, tx, question.ExamID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position),0)+1 FROM questions WHERE exam_id=$1`, question.ExamID).Scan(&question.Position); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO questions (exam_id,domain_id,prompt,scenario,explanation,difficulty,position) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, question.ExamID, question.DomainID, question.Prompt, question.Scenario, question.Explanation, question.Difficulty, question.Position).Scan(&question.ID)
	})
	return question, err
}

func (r *QuestionRepository) Update(ctx context.Context, question exams.Question) error {
	if err := question.ValidateInput(); err != nil {
		return err
	}
	cmd, err := r.store.Pool.Exec(ctx, `UPDATE questions q SET domain_id=$2,prompt=$3,scenario=$4,explanation=$5,difficulty=$6 FROM exams e WHERE q.id=$1 AND e.id=q.exam_id AND e.status='draft'`, question.ID, question.DomainID, strings.TrimSpace(question.Prompt), strings.TrimSpace(question.Scenario), strings.TrimSpace(question.Explanation), question.Difficulty)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft question not found")
	}
	return nil
}

func (r *QuestionRepository) Patch(ctx context.Context, question exams.Question, options *[]exams.Option) error {
	if err := question.ValidateInput(); err != nil {
		return err
	}
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var examID string
		var status exams.Status
		var version int
		if err := tx.QueryRow(ctx, `SELECT exam_id FROM questions WHERE id=$1`, question.ID).Scan(&examID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrQuestionNotFound
			}
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT status, version FROM exams WHERE id=$1 FOR UPDATE`, examID).Scan(&status, &version); err != nil {
			return err
		}
		if status == exams.StatusArchived {
			return ErrArchivedQuestionCannotPatch
		}
		// Update question without draft check
		cmd, err := tx.Exec(ctx, `UPDATE questions SET domain_id=$2,prompt=$3,scenario=$4,explanation=$5,difficulty=$6 WHERE id=$1`, question.ID, question.DomainID, strings.TrimSpace(question.Prompt), strings.TrimSpace(question.Scenario), strings.TrimSpace(question.Explanation), question.Difficulty)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() != 1 {
			return ErrQuestionNotFound
		}
		if options != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM question_options WHERE question_id=$1`, question.ID); err != nil {
				return err
			}
			for idx, opt := range *options {
				if err := opt.ValidateInput(); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO question_options (question_id,option_key,text,explanation,is_correct,position) VALUES ($1,$2,$3,$4,$5,$6)`, question.ID, opt.Key, strings.TrimSpace(opt.Text), strings.TrimSpace(opt.Explanation), opt.IsCorrect, idx+1); err != nil {
					return err
				}
			}
		}
		if status == exams.StatusPublished {
			var raw json.RawMessage
			var versionID string
			if err := tx.QueryRow(ctx, `SELECT id, snapshot FROM exam_versions WHERE exam_id=$1 AND version=$2 FOR UPDATE`, examID, version).Scan(&versionID, &raw); err != nil {
				return err
			}
			var snap exams.Snapshot
			if err := json.Unmarshal(raw, &snap); err != nil {
				return fmt.Errorf("decode snapshot: %w", err)
			}
			// Find and patch the question in snapshot
			found := false
			for i, q := range snap.Questions {
				if q.ID == question.ID {
					snap.Questions[i].DomainID = question.DomainID
					snap.Questions[i].Prompt = question.Prompt
					snap.Questions[i].Scenario = question.Scenario
					snap.Questions[i].Explanation = question.Explanation
					snap.Questions[i].Difficulty = question.Difficulty
					if options != nil {
						opts := make([]exams.SnapshotOption, 0, len(*options))
						for idx, o := range *options {
							opts = append(opts, exams.SnapshotOption{ID: o.ID, Key: o.Key, Text: o.Text, Explanation: o.Explanation, IsCorrect: o.IsCorrect, Position: idx + 1})
						}
						// If options had no IDs (new), we need to fetch them back
						if len(opts) > 0 && opts[0].ID == "" {
							rows, err := tx.Query(ctx, `SELECT id, option_key, text, explanation, is_correct, position FROM question_options WHERE question_id=$1 ORDER BY position`, question.ID)
							if err != nil {
								return err
							}
							opts = opts[:0]
							for rows.Next() {
								var so exams.SnapshotOption
								var qid string
								if err := rows.Scan(&so.ID, &so.Key, &so.Text, &so.Explanation, &so.IsCorrect, &so.Position); err != nil {
									rows.Close()
									return err
								}
								_ = qid
								opts = append(opts, so)
							}
							rows.Close()
							if err := rows.Err(); err != nil {
								return err
							}
						}
						snap.Questions[i].Options = opts
					}
					found = true
					break
				}
			}
			if !found {
				return ErrQuestionNotFoundInSnapshot
			}
			snap.Version = version + 1
			updated, err := json.Marshal(snap)
			if err != nil {
				return fmt.Errorf("marshal snapshot: %w", err)
			}
			var newVersionID string
			if err := tx.QueryRow(ctx, `INSERT INTO exam_versions (exam_id, version, snapshot) VALUES ($1,$2,$3) RETURNING id`, examID, snap.Version, updated).Scan(&newVersionID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE exams SET version=$2, updated_at=now() WHERE id=$1`, examID, snap.Version); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *QuestionRepository) Delete(ctx context.Context, id string) error {
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var examID string
		if err := tx.QueryRow(ctx, `DELETE FROM questions q USING exams e WHERE q.id=$1 AND e.id=q.exam_id AND e.status='draft' RETURNING q.exam_id`, id).Scan(&examID); err != nil {
			return err
		}
		return compactQuestionPositions(ctx, tx, examID)
	})
}

func (r *QuestionRepository) Load(ctx context.Context, id string) (exams.Question, error) {
	var question exams.Question
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT id,exam_id,domain_id,prompt,scenario,explanation,difficulty,position FROM questions WHERE id=$1`, id)
		if err := row.Scan(&question.ID, &question.ExamID, &question.DomainID, &question.Prompt, &question.Scenario, &question.Explanation, &question.Difficulty, &question.Position); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return exams.Question{}, err
	}
	rows, err := r.store.Pool.Query(ctx, `SELECT id,question_id,option_key,text,explanation,is_correct,position FROM question_options WHERE question_id=$1 ORDER BY position`, id)
	if err != nil {
		return exams.Question{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var option exams.Option
		if err := rows.Scan(&option.ID, &option.QuestionID, &option.Key, &option.Text, &option.Explanation, &option.IsCorrect, &option.Position); err != nil {
			return exams.Question{}, err
		}
		question.Options = append(question.Options, option)
	}
	return question, rows.Err()
}

func (r *QuestionRepository) Reorder(ctx context.Context, examID string, questionIDs []string) error {
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireDraftExam(ctx, tx, examID); err != nil {
			return err
		}
		return reorder(ctx, tx, `questions`, `exam_id`, examID, questionIDs)
	})
}

func (r *OptionRepository) Create(ctx context.Context, option exams.Option) (exams.Option, error) {
	if err := option.ValidateInput(); err != nil {
		return exams.Option{}, err
	}
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireDraftQuestion(ctx, tx, option.QuestionID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position),0)+1 FROM question_options WHERE question_id=$1`, option.QuestionID).Scan(&option.Position); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO question_options (question_id,option_key,text,explanation,is_correct,position) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, option.QuestionID, option.Key, strings.TrimSpace(option.Text), strings.TrimSpace(option.Explanation), option.IsCorrect, option.Position).Scan(&option.ID)
	})
	return option, err
}

func (r *OptionRepository) Update(ctx context.Context, option exams.Option) error {
	if err := option.ValidateInput(); err != nil {
		return err
	}
	cmd, err := r.store.Pool.Exec(ctx, `UPDATE question_options o SET option_key=$2,text=$3,explanation=$4,is_correct=$5 FROM questions q JOIN exams e ON e.id=q.exam_id WHERE o.id=$1 AND q.id=o.question_id AND e.status='draft'`, option.ID, option.Key, strings.TrimSpace(option.Text), strings.TrimSpace(option.Explanation), option.IsCorrect)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft option not found")
	}
	return nil
}

func (r *OptionRepository) Delete(ctx context.Context, id string) error {
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var questionID string
		if err := tx.QueryRow(ctx, `DELETE FROM question_options o USING questions q, exams e WHERE o.id=$1 AND q.id=o.question_id AND e.id=q.exam_id AND e.status='draft' RETURNING o.question_id`, id).Scan(&questionID); err != nil {
			return err
		}
		return compactOptionPositions(ctx, tx, questionID)
	})
}

func (r *OptionRepository) Reorder(ctx context.Context, questionID string, optionIDs []string) error {
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireDraftQuestion(ctx, tx, questionID); err != nil {
			return err
		}
		return reorder(ctx, tx, `question_options`, `question_id`, questionID, optionIDs)
	})
}

func (r *QuestionReferenceRepository) Create(ctx context.Context, reference exams.Reference) (exams.Reference, error) {
	if err := reference.ValidateInput(); err != nil {
		return exams.Reference{}, err
	}
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireDraftQuestion(ctx, tx, reference.QuestionID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position),0)+1 FROM question_references WHERE question_id=$1`, reference.QuestionID).Scan(&reference.Position); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO question_references (question_id,title,url,citation,position) VALUES ($1,$2,$3,$4,$5) RETURNING id`, reference.QuestionID, reference.Title, nullable(reference.URL), reference.Citation, reference.Position).Scan(&reference.ID)
	})
	return reference, err
}

func (r *QuestionReferenceRepository) Update(ctx context.Context, reference exams.Reference) error {
	if err := reference.ValidateInput(); err != nil {
		return err
	}
	cmd, err := r.store.Pool.Exec(ctx, `UPDATE question_references r SET title=$2,url=$3,citation=$4 FROM questions q JOIN exams e ON e.id=q.exam_id WHERE r.id=$1 AND q.id=r.question_id AND e.status='draft'`, reference.ID, strings.TrimSpace(reference.Title), nullable(reference.URL), strings.TrimSpace(reference.Citation))
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft reference not found")
	}
	return nil
}

func (r *QuestionReferenceRepository) Delete(ctx context.Context, id string) error {
	cmd, err := r.store.Pool.Exec(ctx, `DELETE FROM question_references r USING questions q, exams e WHERE r.id=$1 AND q.id=r.question_id AND e.id=q.exam_id AND e.status='draft'`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft reference not found")
	}
	return nil
}

func (r *QuestionReferenceRepository) Reorder(ctx context.Context, questionID string, referenceIDs []string) error {
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireDraftQuestion(ctx, tx, questionID); err != nil {
			return err
		}
		return reorder(ctx, tx, `question_references`, `question_id`, questionID, referenceIDs)
	})
}

func loadExamForUpdate(ctx context.Context, tx pgx.Tx, id string) (exams.Exam, error) {
	var exam exams.Exam
	err := tx.QueryRow(ctx, `SELECT id,author_id,title,slug,description,difficulty,time_limit_minutes,pass_percentage,status,version,published_at FROM exams WHERE id=$1 FOR UPDATE`, id).Scan(&exam.ID, &exam.AuthorID, &exam.Title, &exam.Slug, &exam.Description, &exam.Difficulty, &exam.TimeLimitMinutes, &exam.PassPercentage, &exam.Status, &exam.Version, &exam.PublishedAt)
	if err != nil {
		return exams.Exam{}, err
	}
	questions, err := loadQuestions(ctx, tx, id)
	if err != nil {
		return exams.Exam{}, err
	}
	exam.Questions = questions
	return exam, nil
}

func loadQuestions(ctx context.Context, db DBTX, examID string) ([]exams.Question, error) {
	rows, err := db.Query(ctx, `SELECT q.id,q.exam_id,q.domain_id,q.prompt,q.scenario,q.explanation,q.difficulty,q.position,COALESCE(o.id::text,''),COALESCE(o.question_id::text,''),COALESCE(o.option_key,''),COALESCE(o.text,''),COALESCE(o.explanation,''),COALESCE(o.is_correct,false),COALESCE(o.position,0) FROM questions q LEFT JOIN question_options o ON o.question_id=q.id WHERE q.exam_id=$1 ORDER BY q.position,o.position`, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []exams.Question{}
	byID := map[string]int{}
	for rows.Next() {
		var q exams.Question
		var o exams.Option
		if err := rows.Scan(&q.ID, &q.ExamID, &q.DomainID, &q.Prompt, &q.Scenario, &q.Explanation, &q.Difficulty, &q.Position, &o.ID, &o.QuestionID, &o.Key, &o.Text, &o.Explanation, &o.IsCorrect, &o.Position); err != nil {
			return nil, err
		}
		index, ok := byID[q.ID]
		if !ok {
			index = len(items)
			byID[q.ID] = index
			items = append(items, q)
		}
		if o.ID != "" {
			items[index].Options = append(items[index].Options, o)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		refs, err := loadReferences(ctx, db, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].References = refs
	}
	return items, nil
}

func loadReferences(ctx context.Context, db DBTX, questionID string) ([]exams.Reference, error) {
	rows, err := db.Query(ctx, `SELECT id,question_id,title,COALESCE(url,''),citation,position FROM question_references WHERE question_id=$1 ORDER BY position`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []exams.Reference
	for rows.Next() {
		var ref exams.Reference
		if err := rows.Scan(&ref.ID, &ref.QuestionID, &ref.Title, &ref.URL, &ref.Citation, &ref.Position); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
func requireDraftExam(ctx context.Context, db DBTX, id string) error {
	var locked string
	if err := db.QueryRow(ctx, `SELECT id FROM exams WHERE id=$1 AND status='draft' FOR UPDATE`, id).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("draft exam not found")
		}
		return err
	}
	return nil
}
func requireDraftQuestion(ctx context.Context, db DBTX, id string) error {
	var locked string
	if err := db.QueryRow(ctx, `SELECT q.id FROM questions q JOIN exams e ON e.id=q.exam_id WHERE q.id=$1 AND e.status='draft' FOR UPDATE OF q,e`, id).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("draft question not found")
		}
		return err
	}
	return nil
}
func reorder(ctx context.Context, tx pgx.Tx, table, foreignKey, parentID string, ids []string) error {
	if len(ids) == 0 {
		return errors.New("reorder requires at least one id")
	}
	var count int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s=$1`, table, foreignKey), parentID).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return errors.New("reorder must include every child exactly once")
	}
	seen := map[string]struct{}{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			return errors.New("reorder contains duplicate ids")
		}
		seen[id] = struct{}{}
	}
	cmd, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET position=position+1000000 WHERE %s=$1`, table, foreignKey), parentID)
	if err != nil {
		return err
	}
	if int(cmd.RowsAffected()) != count {
		return errors.New("reorder target changed")
	}
	for i, id := range ids {
		cmd, err = tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET position=$1 WHERE id=$2 AND %s=$3`, table, foreignKey), i+1, id, parentID)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() != 1 {
			return errors.New("reorder id does not belong to parent")
		}
	}
	return nil
}
func compactQuestionPositions(ctx context.Context, tx pgx.Tx, examID string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM questions WHERE exam_id=$1 ORDER BY position`, examID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return reorder(ctx, tx, `questions`, `exam_id`, examID, ids)
}
func compactOptionPositions(ctx context.Context, tx pgx.Tx, questionID string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM question_options WHERE question_id=$1 ORDER BY position`, questionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return reorder(ctx, tx, `question_options`, `question_id`, questionID, ids)
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *ExamRepository) ListPublishedExams(ctx context.Context) ([]exams.PublishedExamSummary, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	rows, err := r.store.Pool.Query(ctx, `SELECT e.id, e.title, e.slug, e.description, e.difficulty, e.time_limit_minutes, e.pass_percentage, e.published_at,
		COALESCE(q.count, 0), COALESCE(q.domain_ids, ARRAY[]::uuid[])
		FROM exams e
		LEFT JOIN (
			SELECT exam_id, count(*) AS count, array_agg(DISTINCT domain_id) AS domain_ids
			FROM questions GROUP BY exam_id
		) q ON q.exam_id = e.id
		WHERE e.status = 'published' ORDER BY e.published_at DESC, e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []exams.PublishedExamSummary{}
	for rows.Next() {
		var item exams.PublishedExamSummary
		if err := rows.Scan(&item.ID, &item.Title, &item.Slug, &item.Description, &item.Difficulty, &item.TimeLimitMinutes, &item.PassPercentage, &item.PublishedAt, &item.QuestionCount, &item.DomainIDs); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *ExamRepository) PublishedExamBySlug(ctx context.Context, slug string) (exams.PublishedExam, error) {
	if slug == "" {
		return exams.PublishedExam{}, ErrPublishedExamNotFound
	}
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var exam exams.PublishedExam
	var raw json.RawMessage
	err := r.store.Pool.QueryRow(ctx, `SELECT e.id, e.title, e.slug, e.description, e.difficulty, e.time_limit_minutes, e.pass_percentage, e.published_at, e.version, ev.snapshot
		FROM exams e JOIN exam_versions ev ON ev.exam_id = e.id AND ev.version = e.version
		WHERE e.status = 'published' AND e.slug = $1`, slug).Scan(&exam.ID, &exam.Title, &exam.Slug, &exam.Description, &exam.Difficulty, &exam.TimeLimitMinutes, &exam.PassPercentage, &exam.PublishedAt, &exam.Version, &raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return exams.PublishedExam{}, ErrPublishedExamNotFound
		}
		return exams.PublishedExam{}, err
	}
	var snapshot exams.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return exams.PublishedExam{}, fmt.Errorf("decode published exam snapshot: %w", err)
	}
	exam.Questions = publishedQuestionsFromSnapshot(snapshot)
	return exam, nil
}

func (r *ExamRepository) PublishedSnapshotBySlug(ctx context.Context, slug string) (exams.Snapshot, error) {
	if slug == "" {
		return exams.Snapshot{}, ErrPublishedExamNotFound
	}
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var raw json.RawMessage
	err := r.store.Pool.QueryRow(ctx, `SELECT ev.snapshot
		FROM exams e JOIN exam_versions ev ON ev.exam_id = e.id AND ev.version = e.version
		WHERE e.status = 'published' AND e.slug = $1`, slug).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return exams.Snapshot{}, ErrPublishedExamNotFound
		}
		return exams.Snapshot{}, err
	}
	var snapshot exams.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return exams.Snapshot{}, fmt.Errorf("decode published exam snapshot: %w", err)
	}
	return snapshot, nil
}

func (r *ExamRepository) PublishedPracticeQuestions(ctx context.Context, difficulty string, domainIDs []string) ([]exams.PracticeQuestion, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	rows, err := r.store.Pool.Query(ctx, `SELECT e.slug, ev.snapshot
		FROM exams e JOIN exam_versions ev ON ev.exam_id = e.id AND ev.version = e.version
		WHERE e.status = 'published' ORDER BY e.published_at, e.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domainSet := map[string]struct{}{}
	for _, id := range domainIDs {
		if id != "" {
			domainSet[id] = struct{}{}
		}
	}
	items := []exams.PracticeQuestion{}
	for rows.Next() {
		var slug string
		var raw json.RawMessage
		if err := rows.Scan(&slug, &raw); err != nil {
			return nil, err
		}
		var snapshot exams.Snapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return nil, fmt.Errorf("decode published exam snapshot: %w", err)
		}
		for _, question := range publishedQuestionsFromSnapshot(snapshot) {
			if difficulty != "" && question.Difficulty != exams.Difficulty(difficulty) {
				continue
			}
			if len(domainSet) > 0 {
				if _, ok := domainSet[question.DomainID]; !ok {
					continue
				}
			}
			items = append(items, exams.PracticeQuestion{ExamSlug: slug, ID: question.ID, DomainID: question.DomainID, Prompt: question.Prompt, Scenario: question.Scenario, Difficulty: question.Difficulty, Position: question.Position, Options: question.Options})
		}
	}
	return items, rows.Err()
}

func publishedQuestionsFromSnapshot(snapshot exams.Snapshot) []exams.PublishedQuestion {
	questions := make([]exams.PublishedQuestion, 0, len(snapshot.Questions))
	for _, question := range snapshot.Questions {
		item := exams.PublishedQuestion{ID: question.ID, DomainID: question.DomainID, Prompt: question.Prompt, Scenario: question.Scenario, Explanation: question.Explanation, Difficulty: question.Difficulty, Position: question.Position}
		for _, option := range question.Options {
			item.Options = append(item.Options, exams.PublishedOption{ID: option.ID, Key: option.Key, Text: option.Text, Explanation: option.Explanation, IsCorrect: option.IsCorrect, Position: option.Position})
		}
		for _, reference := range question.References {
			item.References = append(item.References, exams.PublishedReference{Title: reference.Title, URL: reference.URL, Citation: reference.Citation, Position: reference.Position})
		}
		questions = append(questions, item)
	}
	return questions
}

func loadPublishedQuestions(ctx context.Context, db DBTX, examID string) ([]exams.PublishedQuestion, error) {
	rows, err := db.Query(ctx, `SELECT q.id, q.domain_id, q.prompt, q.scenario, q.difficulty, q.position, COALESCE(o.id::text,''), COALESCE(o.option_key,''), COALESCE(o.text,''), COALESCE(o.position,0) FROM questions q LEFT JOIN question_options o ON o.question_id = q.id WHERE q.exam_id = $1 ORDER BY q.position, o.position`, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []exams.PublishedQuestion{}
	byID := map[string]int{}
	for rows.Next() {
		var q exams.PublishedQuestion
		var optionID, optionKey, optionText string
		var optionPosition int
		if err := rows.Scan(&q.ID, &q.DomainID, &q.Prompt, &q.Scenario, &q.Difficulty, &q.Position, &optionID, &optionKey, &optionText, &optionPosition); err != nil {
			return nil, err
		}
		index, ok := byID[q.ID]
		if !ok {
			index = len(items)
			byID[q.ID] = index
			items = append(items, q)
		}
		if optionID != "" {
			items[index].Options = append(items[index].Options, exams.PublishedOption{ID: optionID, Key: optionKey, Text: optionText, Position: optionPosition})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		refs, err := loadPublishedReferences(ctx, db, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].References = refs
	}
	return items, nil
}

func loadPublishedReferences(ctx context.Context, db DBTX, questionID string) ([]exams.PublishedReference, error) {
	rows, err := db.Query(ctx, `SELECT title, COALESCE(url, ''), citation, position FROM question_references WHERE question_id = $1 ORDER BY position`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []exams.PublishedReference
	for rows.Next() {
		var ref exams.PublishedReference
		if err := rows.Scan(&ref.Title, &ref.URL, &ref.Citation, &ref.Position); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (r *ExamRepository) LoadForUpdate(ctx context.Context, id string) (exams.Exam, error) {
	return r.Load(ctx, id)
}

func (r *ExamRepository) LoadBySlug(ctx context.Context, slug string) (exams.Exam, error) {
	var id string
	if err := r.store.Pool.QueryRow(ctx, `SELECT id FROM exams WHERE slug = $1`, slug).Scan(&id); err != nil {
		return exams.Exam{}, err
	}
	return r.Load(ctx, id)
}

func (r *ExamRepository) storeQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.store.QueryTimeout
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}
