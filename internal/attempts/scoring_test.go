package attempts

import (
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/exams"
)

func sampleSnapshot() exams.Snapshot {
	return exams.Snapshot{
		ExamID:           "exam-1",
		Version:          1,
		Title:            "Sample",
		Slug:             "sample",
		Description:      "desc",
		Difficulty:       "easy",
		TimeLimitMinutes: 30,
		PassPercentage:   60.0,
		QuestionIDs:      []string{"q1", "q2", "q3"},
		Questions: []exams.SnapshotQuestion{
			{ID: "q1", Position: 1, Explanation: "Explain q1", Options: []exams.SnapshotOption{
				{ID: "q1-a", Key: "A", Position: 1},
				{ID: "q1-b", Key: "B", IsCorrect: true, Position: 2},
			}},
			{ID: "q2", Position: 2, Explanation: "Explain q2", Options: []exams.SnapshotOption{
				{ID: "q2-a", Key: "A", IsCorrect: true, Position: 1},
				{ID: "q2-b", Key: "B", Position: 2},
			}},
			{ID: "q3", Position: 3, Explanation: "Explain q3", Options: []exams.SnapshotOption{
				{ID: "q3-a", Key: "A", IsCorrect: true, Position: 1},
				{ID: "q3-b", Key: "B", Position: 2},
			}},
		},
	}
}

func TestScoreAllCorrect(t *testing.T) {
	snapshot := sampleSnapshot()
	answers := map[string]Answer{
		"q1": {SelectedOptionIDs: []string{"q1-b"}},
		"q2": {SelectedOptionIDs: []string{"q2-a"}},
		"q3": {SelectedOptionIDs: []string{"q3-a"}},
	}
	result := Score(snapshot, answers)
	if result.CorrectCount != 3 || result.QuestionCount != 3 || result.ScorePercentage != 100 || !result.Passed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestScorePartialCorrect(t *testing.T) {
	snapshot := sampleSnapshot()
	answers := map[string]Answer{
		"q1": {SelectedOptionIDs: []string{"q1-b"}},
		"q2": {SelectedOptionIDs: []string{"q2-b"}},
		"q3": {SelectedOptionIDs: []string{"q3-a"}},
	}
	result := Score(snapshot, answers)
	if result.CorrectCount != 2 || result.QuestionCount != 3 || result.ScorePercentage != 66.67 || !result.Passed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestScoreNoAnswers(t *testing.T) {
	snapshot := sampleSnapshot()
	result := Score(snapshot, map[string]Answer{})
	if result.CorrectCount != 0 || result.QuestionCount != 3 || result.ScorePercentage != 0 || result.Passed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestScoreBelowPass(t *testing.T) {
	snapshot := sampleSnapshot()
	snapshot.PassPercentage = 90
	answers := map[string]Answer{
		"q1": {SelectedOptionIDs: []string{"q1-b"}},
		"q2": {SelectedOptionIDs: []string{"q2-b"}},
		"q3": {SelectedOptionIDs: []string{"q3-a"}},
	}
	result := Score(snapshot, answers)
	if result.Passed {
		t.Fatalf("expected failed result, got %+v", result)
	}
}

func TestResultFromAttempt(t *testing.T) {
	correct := 1
	score := 50.0
	passed := true
	attempt := Attempt{
		QuestionCount: 2,
		CorrectCount:  &correct,
		ScorePercent:  &score,
		Passed:        &passed,
		StartedAt:     time.Now(),
	}
	result := ResultFromAttempt(attempt)
	if result.CorrectCount != 1 || result.ScorePercentage != 50 || !result.Passed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestResultFromAttemptInProgress(t *testing.T) {
	attempt := Attempt{QuestionCount: 2}
	result := ResultFromAttempt(attempt)
	if result.CorrectCount != 0 || result.ScorePercentage != 0 || result.Passed {
		t.Fatalf("unexpected result: %+v", result)
	}
}
