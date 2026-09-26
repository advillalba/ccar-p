package attempts

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/exams"
)

type stubSnapshotLoader struct {
	snapshot  exams.Snapshot
	versionID string
	err       error
	calls     int
}

func (s *stubSnapshotLoader) CurrentSnapshot(_ context.Context, _ string) (exams.Snapshot, string, error) {
	s.calls++
	return s.snapshot, s.versionID, s.err
}

type stubTx struct {
	repo *stubRepository
	id   string
}

func (t *stubTx) Attempt() (Attempt, error) {
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	attempt, ok := t.repo.attempts[t.id]
	if !ok {
		return Attempt{}, errors.New("not found")
	}
	return attempt, nil
}

func (t *stubTx) Answers() (map[string]Answer, error) {
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	bag, ok := t.repo.answers[t.id]
	if !ok {
		return map[string]Answer{}, nil
	}
	copy := map[string]Answer{}
	for k, v := range bag {
		copy[k] = v
	}
	return copy, nil
}

func (t *stubTx) MarkCompleted(result Result) error {
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	attempt := t.repo.attempts[t.id]
	if attempt.Status == StatusCompleted {
		return ErrAlreadyCompleted
	}
	correct := result.CorrectCount
	score := result.ScorePercentage
	passed := result.Passed
	now := time.Now().UTC()
	attempt.Status = StatusCompleted
	attempt.CorrectCount = &correct
	attempt.ScorePercent = &score
	attempt.Passed = &passed
	attempt.CompletedAt = &now
	t.repo.attempts[t.id] = attempt
	return nil
}

type stubRepository struct {
	mu sync.Mutex

	attempts map[string]Attempt
	answers  map[string]map[string]Answer

	createErr error
}

func newStubRepository() *stubRepository {
	return &stubRepository{
		attempts: map[string]Attempt{},
		answers:  map[string]map[string]Answer{},
	}
}

func (s *stubRepository) Create(_ context.Context, owner Owner, examID, examVersionID string, snapshot exams.Snapshot) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return Attempt{}, s.createErr
	}
	id := "attempt-" + owner.UserID + "-" + examID
	attempt := Attempt{ID: id, UserID: owner.UserID, GuestTokenHash: owner.GuestTokenHash, ExamID: examID, ExamVersionID: examVersionID, Status: StatusInProgress, QuestionCount: len(snapshot.Questions), Snapshot: snapshot, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	s.attempts[id] = attempt
	s.answers[id] = map[string]Answer{}
	return attempt, nil
}

func (s *stubRepository) FindByID(_ context.Context, attemptID string, owner Owner) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, ok := s.attempts[attemptID]
	if !ok || !ownerMatches(attempt, owner) {
		return Attempt{}, errors.New("not found")
	}
	return attempt, nil
}

func ownerMatches(attempt Attempt, owner Owner) bool {
	if owner.UserID != "" {
		return attempt.UserID == owner.UserID
	}
	return bytes.Equal(attempt.GuestTokenHash, owner.GuestTokenHash)
}

func (s *stubRepository) ListByUser(_ context.Context, userID string) ([]Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Attempt{}
	for _, a := range s.attempts {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *stubRepository) ListAnswers(_ context.Context, attemptID string) (map[string]Answer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bag, ok := s.answers[attemptID]
	if !ok {
		return map[string]Answer{}, nil
	}
	copy := map[string]Answer{}
	for k, v := range bag {
		copy[k] = v
	}
	return copy, nil
}

func (s *stubRepository) UpsertAnswer(_ context.Context, attemptID, questionID string, optionIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bag, ok := s.answers[attemptID]
	if !ok {
		bag = map[string]Answer{}
		s.answers[attemptID] = bag
	}
	bag[questionID] = Answer{AttemptID: attemptID, QuestionID: questionID, SelectedOptionIDs: optionIDs}
	return nil
}

func (s *stubRepository) BeginAttemptTx(ctx context.Context, attemptID string, owner Owner, fn func(ctx context.Context, tx AttemptTx) error) error {
	s.mu.Lock()
	attempt, ok := s.attempts[attemptID]
	if !ok || !ownerMatches(attempt, owner) {
		s.mu.Unlock()
		return errors.New("not found")
	}
	s.mu.Unlock()
	return fn(ctx, &stubTx{repo: s, id: attemptID})
}

func TestServiceStartAttemptLoadsSnapshot(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempt.Status != StatusInProgress {
		t.Fatalf("expected in_progress, got %s", attempt.Status)
	}
	if loader.calls != 1 {
		t.Fatalf("expected snapshot loader called once")
	}
}

func TestServiceStartAttemptPropagatesUnavailable(t *testing.T) {
	loader := &stubSnapshotLoader{err: errors.New("exam unavailable")}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	_, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestServiceStartAttemptRejectsEmptyOwner(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	if _, err := svc.StartAttempt(context.Background(), Owner{}, "exam-1"); err == nil {
		t.Fatalf("expected empty owner to be rejected")
	}
}

func TestServiceSubmitAnswerRejectsUnknownQuestion(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _, err = svc.SubmitAnswer(context.Background(), Owner{UserID: "u-1"}, attempt.ID, AnswerInput{QuestionID: "missing", SelectedOptionIDs: []string{"x"}})
	if !errors.Is(err, ErrQuestionMissing) {
		t.Fatalf("expected ErrQuestionMissing, got %v", err)
	}
}

func TestServiceSubmitAnswerRejectsWrongOption(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _, err = svc.SubmitAnswer(context.Background(), Owner{UserID: "u-1"}, attempt.ID, AnswerInput{QuestionID: "q1", SelectedOptionIDs: []string{"q2-a"}})
	if !errors.Is(err, ErrCrossQuestion) {
		t.Fatalf("expected ErrCrossQuestion, got %v", err)
	}
}

func TestServiceSubmitAnswerGradesImmediately(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, feedback, err := svc.SubmitAnswer(context.Background(), Owner{UserID: "u-1"}, attempt.ID, AnswerInput{QuestionID: "q1", SelectedOptionIDs: []string{"q1-b"}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !feedback.IsCorrect {
		t.Fatalf("expected correct feedback, got %+v", feedback)
	}
	if feedback.CorrectOptionIDs[0] != "q1-b" {
		t.Fatalf("expected correct option q1-b, got %q", feedback.CorrectOptionIDs[0])
	}
	if feedback.Explanation == "" {
		t.Fatalf("expected explanation in feedback")
	}
}

func TestServiceSubmitAttemptScoresAndCompletes(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, _, err := svc.SubmitAnswer(context.Background(), Owner{UserID: "u-1"}, attempt.ID, AnswerInput{QuestionID: "q1", SelectedOptionIDs: []string{"q1-b"}}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if _, _, err := svc.SubmitAnswer(context.Background(), Owner{UserID: "u-1"}, attempt.ID, AnswerInput{QuestionID: "q2", SelectedOptionIDs: []string{"q2-a"}}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	final, err := svc.SubmitAttempt(context.Background(), Owner{UserID: "u-1"}, attempt.ID)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if final.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", final.Status)
	}
	if final.CorrectCount == nil || *final.CorrectCount != 2 {
		t.Fatalf("expected 2 correct, got %v", final.CorrectCount)
	}
}

func TestServiceSubmitAttemptIdempotent(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, _, err := svc.SubmitAnswer(context.Background(), Owner{UserID: "u-1"}, attempt.ID, AnswerInput{QuestionID: "q1", SelectedOptionIDs: []string{"q1-b"}}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	first, err := svc.SubmitAttempt(context.Background(), Owner{UserID: "u-1"}, attempt.ID)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	second, err := svc.SubmitAttempt(context.Background(), Owner{UserID: "u-1"}, attempt.ID)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if first.ID != second.ID || first.CorrectCount == nil || second.CorrectCount == nil || *first.CorrectCount != *second.CorrectCount {
		t.Fatalf("expected idempotent, got %+v vs %+v", first, second)
	}
}

func TestServiceSubmitAttemptRejectsNonOwner(t *testing.T) {
	loader := &stubSnapshotLoader{snapshot: sampleSnapshot(), versionID: "ver-1"}
	repo := newStubRepository()
	svc := NewService(repo, loader)
	attempt, err := svc.StartAttempt(context.Background(), Owner{UserID: "u-1"}, "exam-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = svc.SubmitAttempt(context.Background(), Owner{UserID: "u-2"}, attempt.ID)
	if err == nil {
		t.Fatalf("expected error")
	}
}
