package exams

import (
	"errors"
	"testing"
)

func TestValidateForPublicationReportsPracticalBlockers(t *testing.T) {
	exam := Exam{Questions: []Question{{ID: "question-1", Position: 2, Options: []Option{{Position: 1, Text: "same"}, {Position: 3, Text: " SAME "}}}}}
	errs := ValidateForPublication(exam)
	if len(errs) < 9 {
		t.Fatalf("got %d errors, want all practical blockers", len(errs))
	}
}

func TestBuildSnapshotIsDetachedAndOrdered(t *testing.T) {
	exam := validExam()
	exam.Questions[0].Position = 2
	exam.Questions = append(exam.Questions, Question{ID: "question-1", DomainID: "domain-1", Prompt: "First?", Explanation: "Because.", Difficulty: "beginner", Position: 1, Options: []Option{{ID: "option-1a", Key: "A", Text: "Correct", Explanation: "Correct reason", IsCorrect: true, Position: 1}, {ID: "option-1b", Key: "B", Text: "Wrong", Explanation: "Wrong reason", Position: 2}}})
	snapshot, err := BuildSnapshot(exam, 3)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 3 || snapshot.Questions[0].ID != "question-1" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.Questions[0].Options[0].Explanation != "Correct reason" {
		t.Fatalf("option explanation was not copied: %#v", snapshot.Questions[0].Options[0])
	}
	exam.Questions[1].Prompt = "changed"
	if snapshot.Questions[0].Prompt == "changed" {
		t.Fatal("snapshot changed with source draft")
	}
}

func TestValidateForPublicationRequiresEveryOptionExplanation(t *testing.T) {
	exam := validExam()
	exam.Questions[0].Options[1].Explanation = " \t"
	errs := ValidateForPublication(exam)
	for _, err := range errs {
		if err.Field == "options.explanation" && err.QuestionID == "question-2" {
			return
		}
	}
	t.Fatalf("missing option explanation error: %#v", errs)
}

func TestOptionValidateInputAllowsBlankExplanationAndLimitsLength(t *testing.T) {
	option := Option{Key: "A", Text: "Choice"}
	if err := option.ValidateInput(); err != nil {
		t.Fatalf("blank draft explanation rejected: %v", err)
	}
	option.Explanation = string(make([]rune, 20001))
	if err := option.ValidateInput(); err == nil {
		t.Fatal("oversized option explanation accepted")
	}
}

func TestArchivedCannotPublishError(t *testing.T) {
	if !errors.Is(ErrArchivedCannotPublish, ErrArchivedCannotPublish) {
		t.Fatal("expected sentinel error")
	}
}

func validExam() Exam {
	return Exam{ID: "exam-1", Title: "Exam", Slug: "exam", Difficulty: "beginner", TimeLimitMinutes: 10, PassPercentage: 70, Questions: []Question{{ID: "question-2", DomainID: "domain-1", Prompt: "Question?", Explanation: "Because.", Difficulty: "beginner", Position: 1, Options: []Option{{ID: "option-a", Key: "A", Text: "Correct", Explanation: "Correct reason", IsCorrect: true, Position: 1}, {ID: "option-b", Key: "B", Text: "Wrong", Explanation: "Wrong reason", Position: 2}}}}}
}
