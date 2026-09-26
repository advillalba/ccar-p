package attempts

import (
	"math"

	"github.com/ccar-p/study-platform/internal/exams"
)

type Result struct {
	CorrectCount    int
	QuestionCount   int
	ScorePercentage float64
	Passed          bool
}

// QuestionCorrectOptionIDs returns the IDs of every correct option, in
// snapshot order.
func QuestionCorrectOptionIDs(question exams.SnapshotQuestion) []string {
	ids := make([]string, 0, 2)
	for _, option := range question.Options {
		if option.IsCorrect {
			ids = append(ids, option.ID)
		}
	}
	return ids
}

// SelectionsAreCorrect reports whether the selected option IDs exactly match
// the question's correct option set. Duplicates and order are irrelevant.
func SelectionsAreCorrect(question exams.SnapshotQuestion, selectedIDs []string) bool {
	correct := QuestionCorrectOptionIDs(question)
	if len(selectedIDs) == 0 || len(selectedIDs) != len(correct) {
		return false
	}
	set := make(map[string]struct{}, len(correct))
	for _, id := range correct {
		set[id] = struct{}{}
	}
	for _, id := range selectedIDs {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

func Score(snapshot exams.Snapshot, answers map[string]Answer) Result {
	correct := 0
	for _, question := range snapshot.Questions {
		answer, ok := answers[question.ID]
		if !ok || len(answer.SelectedOptionIDs) == 0 {
			continue
		}
		if SelectionsAreCorrect(question, answer.SelectedOptionIDs) {
			correct++
		}
	}
	total := len(snapshot.Questions)
	score := 0.0
	if total > 0 {
		score = math.Round(float64(correct)/float64(total)*10000) / 100
	}
	passed := score >= snapshot.PassPercentage
	return Result{CorrectCount: correct, QuestionCount: total, ScorePercentage: score, Passed: passed}
}

func ResultFromAttempt(attempt Attempt) Result {
	if attempt.CorrectCount == nil || attempt.ScorePercent == nil || attempt.Passed == nil {
		return Result{QuestionCount: attempt.QuestionCount}
	}
	return Result{
		CorrectCount:    *attempt.CorrectCount,
		QuestionCount:   attempt.QuestionCount,
		ScorePercentage: *attempt.ScorePercent,
		Passed:          *attempt.Passed,
	}
}
