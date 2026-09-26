package attempts

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/ccar-p/study-platform/internal/exams"
)

type Status string

const (
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

var ErrAlreadyCompleted = errors.New("attempt already completed")

type Attempt struct {
	ID             string
	UserID         string
	GuestTokenHash []byte
	ExamID         string
	ExamVersionID  string
	Status         Status
	QuestionCount  int
	CorrectCount   *int
	ScorePercent   *float64
	Passed         *bool
	StartedAt      time.Time
	CompletedAt    *time.Time
	UpdatedAt      time.Time
	Snapshot       exams.Snapshot
}

// Owner identifies who may act on an attempt. Exactly one of UserID or
// GuestTokenHash is set: UserID for authenticated students, GuestTokenHash
// for anonymous visitors identified by their device cookie.
type Owner struct {
	UserID         string
	GuestTokenHash []byte
}

// Empty reports whether the owner carries no identity at all.
func (o Owner) Empty() bool {
	return o.UserID == "" && len(o.GuestTokenHash) == 0
}

// HashGuestToken derives the stored fingerprint of a guest's bearer token.
func HashGuestToken(token string) []byte {
	if token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

type Answer struct {
	AttemptID         string
	QuestionID        string
	SelectedOptionIDs []string
	IsCorrect         *bool
	AnsweredAt        time.Time
}

func UnmarshalSnapshot(raw json.RawMessage) (exams.Snapshot, error) {
	var snapshot exams.Snapshot
	if len(raw) == 0 {
		return snapshot, errors.New("attempt snapshot is empty")
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func MarshalSnapshot(snapshot exams.Snapshot) (json.RawMessage, error) {
	return json.Marshal(snapshot)
}

type Summary struct {
	ID            string     `json:"id"`
	ExamID        string     `json:"exam_id"`
	ExamSlug      string     `json:"exam_slug"`
	ExamTitle     string     `json:"exam_title"`
	Status        Status     `json:"status"`
	QuestionCount int        `json:"question_count"`
	CorrectCount  *int       `json:"correct_count,omitempty"`
	ScorePercent  *float64   `json:"score_percentage,omitempty"`
	Passed        *bool      `json:"passed,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type ActiveQuestion struct {
	ID                string                    `json:"id"`
	Position          int                       `json:"position"`
	DomainID          string                    `json:"domain_id"`
	Prompt            string                    `json:"prompt"`
	Scenario          string                    `json:"scenario"`
	Difficulty        exams.Difficulty          `json:"difficulty"`
	Options           []ActiveOption            `json:"options"`
	References        []exams.SnapshotReference `json:"references"`
	SelectedOptionIDs []string                  `json:"selected_option_ids,omitempty"`
	IsCorrect         *bool                     `json:"is_correct,omitempty"`
	CorrectOptionIDs  []string                  `json:"correct_option_ids,omitempty"`
}

type ActiveOption struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type ActiveAttemptDetail struct {
	ID            string           `json:"id"`
	Status        Status           `json:"status"`
	Exam          ActiveExamMeta   `json:"exam"`
	QuestionCount int              `json:"question_count"`
	StartedAt     time.Time        `json:"started_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Questions     []ActiveQuestion `json:"questions"`
}

type ActiveExamMeta struct {
	ID               string           `json:"id"`
	Slug             string           `json:"slug"`
	Title            string           `json:"title"`
	Description      string           `json:"description"`
	Difficulty       exams.Difficulty `json:"difficulty"`
	TimeLimitMinutes int              `json:"time_limit_minutes"`
	PassPercentage   float64          `json:"pass_percentage"`
	Version          int              `json:"version"`
}

type CompletedOption struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Text        string `json:"text"`
	Explanation string `json:"explanation"`
	Position    int    `json:"position"`
}

type CompletedQuestion struct {
	ID                string                    `json:"id"`
	Position          int                       `json:"position"`
	DomainID          string                    `json:"domain_id"`
	Prompt            string                    `json:"prompt"`
	Scenario          string                    `json:"scenario,omitempty"`
	Difficulty        exams.Difficulty          `json:"difficulty"`
	Explanation       string                    `json:"explanation"`
	Options           []CompletedOption         `json:"options"`
	References        []exams.SnapshotReference `json:"references"`
	SelectedOptionIDs []string                  `json:"selected_option_ids"`
	CorrectOptionIDs  []string                  `json:"correct_option_ids"`
	IsCorrect         bool                      `json:"is_correct"`
}

type CompletedAttemptDetail struct {
	ID             string              `json:"id"`
	Status         Status              `json:"status"`
	Exam           ActiveExamMeta      `json:"exam"`
	QuestionCount  int                 `json:"question_count"`
	CorrectCount   int                 `json:"correct_count"`
	ScorePercent   float64             `json:"score_percentage"`
	Passed         bool                `json:"passed"`
	PassPercentage float64             `json:"pass_percentage"`
	StartedAt      time.Time           `json:"started_at"`
	CompletedAt    time.Time           `json:"completed_at"`
	Questions      []CompletedQuestion `json:"questions"`
}

// AnswerFeedback reports the immediate grading of a single submitted
// answer. It is returned by SubmitAnswer so the reader learns whether a
// selection is correct without waiting for submission.
type AnswerFeedback struct {
	QuestionID        string
	SelectedOptionIDs []string
	CorrectOptionIDs  []string
	IsCorrect         bool
	Explanation       string
}
