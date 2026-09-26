package exams

import "time"

type PublishedExam struct {
	ID               string
	Title            string
	Slug             string
	Description      string
	Difficulty       Difficulty
	TimeLimitMinutes int
	PassPercentage   float64
	PublishedAt      time.Time
	Version          int
	Questions        []PublishedQuestion
}

type PublishedExamSummary struct {
	ID               string
	Title            string
	Slug             string
	Description      string
	Difficulty       Difficulty
	TimeLimitMinutes int
	PassPercentage   float64
	QuestionCount    int
	DomainIDs        []string
	PublishedAt      time.Time
}

type PublishedQuestion struct {
	ID          string
	DomainID    string
	Prompt      string
	Scenario    string
	Explanation string
	Difficulty  Difficulty
	Position    int
	Options     []PublishedOption
	References  []PublishedReference
}

type PublishedOption struct {
	ID          string
	Key         string
	Text        string
	Explanation string
	IsCorrect   bool
	Position    int
}

type PublishedReference struct {
	Title    string
	URL      string
	Citation string
	Position int
}

// PracticeQuestion is one published question exposed to the continuous
// practice mode, tagged with the exam it came from.
type PracticeQuestion struct {
	ExamSlug   string
	ID         string
	DomainID   string
	Prompt     string
	Scenario   string
	Difficulty Difficulty
	Position   int
	Options    []PublishedOption
}
