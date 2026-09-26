package attempts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/ccar-p/study-platform/internal/exams"
)

var (
	ErrExamUnavailable = errors.New("exam is not available for attempts")
	ErrCrossQuestion   = errors.New("selected option does not belong to question")
	ErrQuestionMissing = errors.New("question does not belong to attempt")
	ErrNotInProgress   = errors.New("attempt is not in progress")
	ErrNoSelection     = errors.New("no option selected")
)

type ExamSnapshotLoader interface {
	CurrentSnapshot(ctx context.Context, examID string) (exams.Snapshot, string, error)
}

type AttemptRepository interface {
	Create(ctx context.Context, owner Owner, examID, examVersionID string, snapshot exams.Snapshot) (Attempt, error)
	FindByID(ctx context.Context, attemptID string, owner Owner) (Attempt, error)
	ListByUser(ctx context.Context, userID string) ([]Attempt, error)
	ListAnswers(ctx context.Context, attemptID string) (map[string]Answer, error)
	UpsertAnswer(ctx context.Context, attemptID, questionID string, optionIDs []string) error
	BeginAttemptTx(ctx context.Context, attemptID string, owner Owner, fn func(ctx context.Context, tx AttemptTx) error) error
}

type AttemptTx interface {
	Attempt() (Attempt, error)
	Answers() (map[string]Answer, error)
	MarkCompleted(result Result) error
}

type Service struct {
	repo   AttemptRepository
	loader ExamSnapshotLoader
}

func NewService(repo AttemptRepository, loader ExamSnapshotLoader) *Service {
	return &Service{repo: repo, loader: loader}
}

func (s *Service) StartAttempt(ctx context.Context, owner Owner, examID string) (Attempt, error) {
	if owner.Empty() || examID == "" {
		return Attempt{}, fmt.Errorf("owner and exam are required")
	}
	snapshot, versionID, err := s.loader.CurrentSnapshot(ctx, examID)
	if err != nil {
		return Attempt{}, err
	}
	return s.repo.Create(ctx, owner, examID, versionID, snapshot)
}

type AnswerInput struct {
	QuestionID        string
	SelectedOptionIDs []string
}

// CanonicalSelection dedups and sorts the selected option IDs. When the list
// is empty but a legacy single selection is present, it is wrapped. It
// returns nil when there is nothing to grade.
func CanonicalSelection(selectedIDs []string, legacyID string) []string {
	ids := make([]string, 0, len(selectedIDs)+1)
	if legacyID != "" {
		ids = append(ids, legacyID)
	}
	for _, id := range selectedIDs {
		if id == "" {
			continue
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func (s *Service) SubmitAnswer(ctx context.Context, owner Owner, attemptID string, input AnswerInput) (Attempt, AnswerFeedback, error) {
	attempt, err := s.repo.FindByID(ctx, attemptID, owner)
	if err != nil {
		return Attempt{}, AnswerFeedback{}, err
	}
	if attempt.Status != StatusInProgress {
		return Attempt{}, AnswerFeedback{}, ErrNotInProgress
	}
	question, ok := lookupQuestion(attempt.Snapshot, input.QuestionID)
	if !ok {
		return Attempt{}, AnswerFeedback{}, ErrQuestionMissing
	}
	selection := CanonicalSelection(input.SelectedOptionIDs, "")
	if len(selection) == 0 {
		return Attempt{}, AnswerFeedback{}, ErrNoSelection
	}
	for _, id := range selection {
		if !optionBelongs(question, id) {
			return Attempt{}, AnswerFeedback{}, ErrCrossQuestion
		}
	}
	if err := s.repo.UpsertAnswer(ctx, attemptID, input.QuestionID, selection); err != nil {
		return Attempt{}, AnswerFeedback{}, err
	}
	feedback := gradeAnswer(question, selection)
	attempt, err = s.repo.FindByID(ctx, attemptID, owner)
	if err != nil {
		return Attempt{}, AnswerFeedback{}, err
	}
	return attempt, feedback, nil
}

// gradeAnswer computes the immediate correctness of a selection against the
// question's snapshot options. A question with several correct options is
// answered correctly when the selection exactly matches that set.
func gradeAnswer(question exams.SnapshotQuestion, selectedOptionIDs []string) AnswerFeedback {
	return AnswerFeedback{
		QuestionID:        question.ID,
		SelectedOptionIDs: selectedOptionIDs,
		CorrectOptionIDs:  QuestionCorrectOptionIDs(question),
		IsCorrect:         SelectionsAreCorrect(question, selectedOptionIDs),
		Explanation:       question.Explanation,
	}
}

func (s *Service) SubmitAttempt(ctx context.Context, owner Owner, attemptID string) (Attempt, error) {
	var final Attempt
	err := s.repo.BeginAttemptTx(ctx, attemptID, owner, func(ctx context.Context, tx AttemptTx) error {
		current, err := tx.Attempt()
		if err != nil {
			return err
		}
		if current.Status == StatusCompleted {
			final = current
			return nil
		}
		if current.Status != StatusInProgress {
			return ErrNotInProgress
		}
		answers, err := tx.Answers()
		if err != nil {
			return err
		}
		result := Score(current.Snapshot, answers)
		if err := tx.MarkCompleted(result); err != nil {
			if errors.Is(err, ErrAlreadyCompleted) {
				reloaded, err := tx.Attempt()
				if err != nil {
					return err
				}
				final = reloaded
				return nil
			}
			return err
		}
		reloaded, err := tx.Attempt()
		if err != nil {
			return err
		}
		final = reloaded
		return nil
	})
	return final, err
}

func (s *Service) ListAttempts(ctx context.Context, userID string) ([]Attempt, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) GetAttempt(ctx context.Context, owner Owner, attemptID string) (Attempt, error) {
	return s.repo.FindByID(ctx, attemptID, owner)
}

func (s *Service) LoadAnswers(ctx context.Context, attemptID string) (map[string]Answer, error) {
	return s.repo.ListAnswers(ctx, attemptID)
}

func lookupQuestion(snapshot exams.Snapshot, questionID string) (exams.SnapshotQuestion, bool) {
	for _, q := range snapshot.Questions {
		if q.ID == questionID {
			return q, true
		}
	}
	return exams.SnapshotQuestion{}, false
}

func optionBelongs(question exams.SnapshotQuestion, optionID string) bool {
	for _, opt := range question.Options {
		if opt.ID == optionID {
			return true
		}
	}
	return false
}
