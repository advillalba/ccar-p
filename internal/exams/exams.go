package exams

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusArchived  Status = "archived"
)

type Difficulty string

const (
	DifficultyBeginner      Difficulty = "beginner"
	DifficultyIntermediate  Difficulty = "intermediate"
	DifficultyAdvanced      Difficulty = "advanced"
	DifficultyExamScenarios Difficulty = "exam_scenarios"
)

type Exam struct {
	ID               string     `json:"id"`
	AuthorID         string     `json:"author_id"`
	Title            string     `json:"title"`
	Slug             string     `json:"slug"`
	Description      string     `json:"description"`
	Difficulty       Difficulty `json:"difficulty"`
	TimeLimitMinutes int        `json:"time_limit_minutes"`
	PassPercentage   float64    `json:"pass_percentage"`
	Status           Status     `json:"status"`
	Version          int        `json:"version"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	Questions        []Question `json:"questions"`
}

type Question struct {
	ID          string      `json:"id"`
	ExamID      string      `json:"exam_id"`
	DomainID    string      `json:"domain_id"`
	Prompt      string      `json:"prompt"`
	Scenario    string      `json:"scenario"`
	Explanation string      `json:"explanation"`
	Difficulty  Difficulty  `json:"difficulty"`
	Position    int         `json:"position"`
	Options     []Option    `json:"options"`
	References  []Reference `json:"references"`
}

type Option struct {
	ID          string `json:"id"`
	QuestionID  string `json:"question_id"`
	Key         string `json:"key"`
	Text        string `json:"text"`
	Explanation string `json:"explanation"`
	IsCorrect   bool   `json:"is_correct"`
	Position    int    `json:"position"`
}

type Reference struct {
	ID         string `json:"id"`
	QuestionID string `json:"question_id"`
	Title      string `json:"title"`
	URL        string `json:"url,omitempty"`
	Citation   string `json:"citation,omitempty"`
	Position   int    `json:"position"`
}

type Snapshot struct {
	ExamID           string             `json:"exam_id"`
	Version          int                `json:"version"`
	Title            string             `json:"title"`
	Slug             string             `json:"slug"`
	Description      string             `json:"description"`
	Difficulty       Difficulty         `json:"difficulty"`
	TimeLimitMinutes int                `json:"time_limit_minutes"`
	PassPercentage   float64            `json:"pass_percentage"`
	QuestionIDs      []string           `json:"question_ids"`
	Questions        []SnapshotQuestion `json:"questions"`
}

type SnapshotQuestion struct {
	ID          string              `json:"id"`
	DomainID    string              `json:"domain_id"`
	Prompt      string              `json:"prompt"`
	Scenario    string              `json:"scenario"`
	Explanation string              `json:"explanation"`
	Difficulty  Difficulty          `json:"difficulty"`
	Position    int                 `json:"position"`
	Options     []SnapshotOption    `json:"options"`
	References  []SnapshotReference `json:"references"`
}

type SnapshotOption struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Text        string `json:"text"`
	Explanation string `json:"explanation"`
	IsCorrect   bool   `json:"is_correct"`
	Position    int    `json:"position"`
}

type SnapshotReference struct {
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	Citation string `json:"citation,omitempty"`
	Position int    `json:"position"`
}

type ValidationError struct {
	Field      string `json:"field"`
	QuestionID string `json:"question_id,omitempty"`
	Message    string `json:"message"`
}

func ValidateForPublication(exam Exam) []ValidationError {
	errs := make([]ValidationError, 0)
	if strings.TrimSpace(exam.Title) == "" {
		errs = append(errs, ValidationError{Field: "title", Message: "title is required"})
	}
	if !validDifficulty(exam.Difficulty) {
		errs = append(errs, ValidationError{Field: "difficulty", Message: "difficulty must be beginner, intermediate, advanced, or exam_scenarios"})
	}
	if len(exam.Questions) == 0 {
		errs = append(errs, ValidationError{Field: "questions", Message: "at least one question is required"})
	}
	if !contiguousQuestionPositions(exam.Questions) {
		errs = append(errs, ValidationError{Field: "questions.position", Message: "question positions must be contiguous starting at 1"})
	}
	for _, question := range exam.Questions {
		if strings.TrimSpace(question.Prompt) == "" {
			errs = append(errs, questionError(question, "prompt", "prompt is required"))
		}
		if strings.TrimSpace(question.DomainID) == "" {
			errs = append(errs, questionError(question, "domain_id", "domain is required"))
		}
		if !validDifficulty(question.Difficulty) {
			errs = append(errs, questionError(question, "difficulty", "difficulty must be beginner, intermediate, advanced, or exam_scenarios"))
		}
		if strings.TrimSpace(question.Explanation) == "" {
			errs = append(errs, questionError(question, "explanation", "explanation is required"))
		}
		if len(question.Options) < 2 {
			errs = append(errs, questionError(question, "options", "at least two options are required"))
		}
		if !contiguousOptionPositions(question.Options) {
			errs = append(errs, questionError(question, "options.position", "option positions must be contiguous starting at 1"))
		}
		correct := 0
		texts := make(map[string]struct{}, len(question.Options))
		for _, option := range question.Options {
			if option.IsCorrect {
				correct++
			}
			text := strings.ToLower(strings.TrimSpace(option.Text))
			if text == "" {
				errs = append(errs, questionError(question, "options.text", "option text is required"))
			} else if _, exists := texts[text]; exists {
				errs = append(errs, questionError(question, "options", "options must have distinct text"))
			} else {
				texts[text] = struct{}{}
			}
			if strings.TrimSpace(option.Explanation) == "" {
				errs = append(errs, questionError(question, "options.explanation", "option explanation is required"))
			}
		}
		if correct < 1 {
			errs = append(errs, questionError(question, "options.is_correct", "at least one option must be correct"))
		}
	}
	return errs
}

func BuildSnapshot(exam Exam, version int) (Snapshot, error) {
	if errs := ValidateForPublication(exam); len(errs) != 0 {
		return Snapshot{}, fmt.Errorf("invalid exam: %w", ValidationErrors(errs))
	}
	snapshot := Snapshot{ExamID: exam.ID, Version: version, Title: exam.Title, Slug: exam.Slug, Description: exam.Description, Difficulty: exam.Difficulty, TimeLimitMinutes: exam.TimeLimitMinutes, PassPercentage: exam.PassPercentage}
	questions := append([]Question(nil), exam.Questions...)
	sort.Slice(questions, func(i, j int) bool { return questions[i].Position < questions[j].Position })
	for _, question := range questions {
		item := SnapshotQuestion{ID: question.ID, DomainID: question.DomainID, Prompt: question.Prompt, Scenario: question.Scenario, Explanation: question.Explanation, Difficulty: question.Difficulty, Position: question.Position}
		snapshot.QuestionIDs = append(snapshot.QuestionIDs, question.ID)
		options := append([]Option(nil), question.Options...)
		sort.Slice(options, func(i, j int) bool { return options[i].Position < options[j].Position })
		for _, option := range options {
			item.Options = append(item.Options, SnapshotOption{ID: option.ID, Key: option.Key, Text: option.Text, Explanation: option.Explanation, IsCorrect: option.IsCorrect, Position: option.Position})
		}
		references := append([]Reference(nil), question.References...)
		sort.Slice(references, func(i, j int) bool { return references[i].Position < references[j].Position })
		for _, reference := range references {
			item.References = append(item.References, SnapshotReference{Title: reference.Title, URL: reference.URL, Citation: reference.Citation, Position: reference.Position})
		}
		snapshot.Questions = append(snapshot.Questions, item)
	}
	return snapshot, nil
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string { return "exam publication validation failed" }

type FieldErrors map[string]string

func (e FieldErrors) Error() string { return "invalid exam input" }

func (e Exam) ValidateInput() error {
	errs := FieldErrors{}
	if strings.TrimSpace(e.Title) == "" || len([]rune(e.Title)) > 200 {
		errs["title"] = "must be between 1 and 200 characters"
	}
	if !validSlug(e.Slug) {
		errs["slug"] = "must use lowercase letters, numbers, and single hyphens"
	}
	if len([]rune(e.Description)) > 2000 {
		errs["description"] = "must not exceed 2000 characters"
	}
	if !validDifficulty(e.Difficulty) {
		errs["difficulty"] = "must be beginner, intermediate, advanced, or exam_scenarios"
	}
	if e.TimeLimitMinutes < 1 || e.TimeLimitMinutes > 1440 {
		errs["time_limit_minutes"] = "must be between 1 and 1440"
	}
	if e.PassPercentage < 0 || e.PassPercentage > 100 {
		errs["pass_percentage"] = "must be between 0 and 100"
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func (q Question) ValidateInput() error {
	errs := FieldErrors{}
	if strings.TrimSpace(q.DomainID) == "" {
		errs["domain_id"] = "is required"
	}
	if strings.TrimSpace(q.Prompt) == "" || len([]rune(q.Prompt)) > 10000 {
		errs["prompt"] = "must be between 1 and 10000 characters"
	}
	if len([]rune(q.Scenario)) > 10000 {
		errs["scenario"] = "must not exceed 10000 characters"
	}
	if len([]rune(q.Explanation)) > 20000 {
		errs["explanation"] = "must not exceed 20000 characters"
	}
	if !validDifficulty(q.Difficulty) {
		errs["difficulty"] = "must be beginner, intermediate, advanced, or exam_scenarios"
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func (o Option) ValidateInput() error {
	errs := FieldErrors{}
	if len(o.Key) != 1 || o.Key[0] < 'A' || o.Key[0] > 'Z' {
		errs["key"] = "must be one uppercase letter"
	}
	if strings.TrimSpace(o.Text) == "" || len([]rune(o.Text)) > 5000 {
		errs["text"] = "must be between 1 and 5000 characters"
	}
	if len([]rune(o.Explanation)) > 20000 {
		errs["explanation"] = "must not exceed 20000 characters"
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func (r Reference) ValidateInput() error {
	errs := FieldErrors{}
	if strings.TrimSpace(r.Title) == "" || len([]rune(r.Title)) > 300 {
		errs["title"] = "must be between 1 and 300 characters"
	}
	if r.URL != "" && !validReferenceURL(r.URL) {
		errs["url"] = "must be an absolute http or https URL"
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validDifficulty(value Difficulty) bool {
	return value == DifficultyBeginner || value == DifficultyIntermediate || value == DifficultyAdvanced || value == DifficultyExamScenarios
}

func validSlug(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, r := range value {
		if r == '-' {
			if previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		previousHyphen = false
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func validReferenceURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func questionError(question Question, field, message string) ValidationError {
	return ValidationError{Field: field, QuestionID: question.ID, Message: message}
}

func contiguousQuestionPositions(items []Question) bool {
	positions := make([]int, len(items))
	for i := range items {
		positions[i] = items[i].Position
	}
	return contiguous(positions)
}

func contiguousOptionPositions(items []Option) bool {
	positions := make([]int, len(items))
	for i := range items {
		positions[i] = items[i].Position
	}
	return contiguous(positions)
}

func contiguous(positions []int) bool {
	sort.Ints(positions)
	for i, position := range positions {
		if position != i+1 {
			return false
		}
	}
	return true
}

var ErrArchivedCannotPublish = errors.New("archived exams cannot be published")
