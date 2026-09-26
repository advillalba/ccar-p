package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/attempts"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/store"
)

type stubExamReader struct {
	exams             []exams.PublishedExamSummary
	examsBySlug       map[string]exams.PublishedExam
	snapshotsBySlug   map[string]exams.Snapshot
	errBySlug         map[string]error
	practiceQuestions []exams.PracticeQuestion
}

func newStubExamReader() *stubExamReader {
	return &stubExamReader{
		examsBySlug:     make(map[string]exams.PublishedExam),
		snapshotsBySlug: make(map[string]exams.Snapshot),
		errBySlug:       make(map[string]error),
	}
}

func (s *stubExamReader) ListPublishedExams(_ context.Context) ([]exams.PublishedExamSummary, error) {
	out := make([]exams.PublishedExamSummary, len(s.exams))
	copy(out, s.exams)
	return out, nil
}

func (s *stubExamReader) PublishedExamBySlug(_ context.Context, slug string) (exams.PublishedExam, error) {
	if err, ok := s.errBySlug[slug]; ok {
		return exams.PublishedExam{}, err
	}
	if exam, ok := s.examsBySlug[slug]; ok {
		return exam, nil
	}
	return exams.PublishedExam{}, store.ErrPublishedExamNotFound
}

func (s *stubExamReader) PublishedSnapshotBySlug(_ context.Context, slug string) (exams.Snapshot, error) {
	if err, ok := s.errBySlug[slug]; ok {
		return exams.Snapshot{}, err
	}
	if snapshot, ok := s.snapshotsBySlug[slug]; ok {
		return snapshot, nil
	}
	return exams.Snapshot{}, store.ErrPublishedExamNotFound
}

func (s *stubExamReader) PublishedPracticeQuestions(_ context.Context, difficulty string, domainIDs []string) ([]exams.PracticeQuestion, error) {
	domainSet := map[string]struct{}{}
	for _, id := range domainIDs {
		domainSet[id] = struct{}{}
	}
	out := make([]exams.PracticeQuestion, 0, len(s.practiceQuestions))
	for _, question := range s.practiceQuestions {
		if difficulty != "" && question.Difficulty != exams.Difficulty(difficulty) {
			continue
		}
		if len(domainSet) > 0 {
			if _, ok := domainSet[question.DomainID]; !ok {
				continue
			}
		}
		out = append(out, question)
	}
	return out, nil
}

func newExamsHandler() (*ExamsHandler, *stubExamReader) {
	reader := newStubExamReader()
	publishedAt := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	reader.exams = []exams.PublishedExamSummary{
		{
			ID:               "exam-1",
			Title:            "Cloud Practitioner Practice Exam",
			Slug:             "cloud-practitioner-practice",
			Description:      "Full-length mock exam.",
			Difficulty:       exams.Difficulty("beginner"),
			TimeLimitMinutes: 90,
			PassPercentage:   70,
			QuestionCount:    2,
			PublishedAt:      publishedAt,
		},
	}
	reader.examsBySlug["cloud-practitioner-practice"] = exams.PublishedExam{
		ID:               "exam-1",
		Title:            "Cloud Practitioner Practice Exam",
		Slug:             "cloud-practitioner-practice",
		Description:      "Full-length mock exam.",
		Difficulty:       exams.Difficulty("beginner"),
		TimeLimitMinutes: 90,
		PassPercentage:   70,
		Version:          1,
		PublishedAt:      publishedAt,
		Questions: []exams.PublishedQuestion{
			{
				ID:         "q-1",
				DomainID:   "domain-cloud",
				Prompt:     "Which service is serverless?",
				Scenario:   "A startup wants to deploy a function without managing servers.",
				Difficulty: exams.Difficulty("beginner"),
				Position:   1,
				Options: []exams.PublishedOption{
					{Key: "A", Text: "EC2", Position: 1},
					{Key: "B", Text: "Lambda", Position: 2},
					{Key: "C", Text: "RDS", Position: 3},
				},
				References: []exams.PublishedReference{
					{Title: "Lambda Guide", URL: "https://example.com/lambda", Citation: "Chapter 2", Position: 1},
				},
			},
		},
	}
	return NewExamsHandler(reader, nil), reader
}

func practiceSnapshot() exams.Snapshot {
	return exams.Snapshot{
		ExamID:           "exam-1",
		Version:          1,
		Title:            "Cloud Practitioner Practice Exam",
		Slug:             "cloud-practitioner-practice",
		Description:      "Full-length mock exam.",
		Difficulty:       exams.Difficulty("beginner"),
		TimeLimitMinutes: 90,
		PassPercentage:   70,
		QuestionIDs:      []string{"q1", "q2"},
		Questions: []exams.SnapshotQuestion{
			{
				ID:          "q1",
				DomainID:    "domain-cloud",
				Prompt:      "Which service is serverless?",
				Scenario:    "A startup wants to deploy a function without managing servers.",
				Explanation: "Lambda runs functions without provisioning servers.",
				Difficulty:  exams.Difficulty("beginner"),
				Position:    1,
				Options: []exams.SnapshotOption{
					{ID: "q1-a", Key: "A", Text: "EC2", Explanation: "EC2 requires managing virtual machines.", IsCorrect: false, Position: 1},
					{ID: "q1-b", Key: "B", Text: "Lambda", Explanation: "Lambda runs code without provisioning servers.", IsCorrect: true, Position: 2},
				},
			},
			{
				ID:          "q2",
				DomainID:    "domain-cloud",
				Prompt:      "Which database scales automatically?",
				Explanation: "DynamoDB scales capacity automatically.",
				Difficulty:  exams.Difficulty("beginner"),
				Position:    2,
				Options: []exams.SnapshotOption{
					{ID: "q2-a", Key: "A", Text: "Oracle", Explanation: "Oracle requires capacity planning.", IsCorrect: false, Position: 1},
					{ID: "q2-b", Key: "B", Text: "DynamoDB", Explanation: "DynamoDB scales on demand.", IsCorrect: true, Position: 2},
					{ID: "q2-c", Key: "C", Text: "Aurora", Explanation: "Aurora scales storage and compute independently.", IsCorrect: true, Position: 3},
				},
			},
		},
	}
}

func postPractice(t *testing.T, slug, body string) (*httptest.ResponseRecorder, *stubExamReader) {
	t.Helper()
	handler, reader := newExamsHandler()
	reader.snapshotsBySlug["cloud-practitioner-practice"] = practiceSnapshot()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+slug+"/practice", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w, reader
}

func TestExamsListEndpointReturnsPublishedOnly(t *testing.T) {
	handler, _ := newExamsHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exams", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	disallowed := []string{"is_correct", "correct_option", "explanation", "answer", "is_true", "author_id"}
	for _, term := range disallowed {
		if strings.Contains(strings.ToLower(body), term) {
			t.Fatalf("response leaked %q: %s", term, body)
		}
	}
	var envelope successEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", body)
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PublicExamListDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid exam list: %s", string(raw))
	}
	if len(dto.Exams) != 1 {
		t.Fatalf("expected 1 exam, got %d", len(dto.Exams))
	}
	exam := dto.Exams[0]
	if exam.Slug != "cloud-practitioner-practice" {
		t.Fatalf("unexpected slug: %s", exam.Slug)
	}
	if exam.QuestionCount != 2 {
		t.Fatalf("expected question_count 2, got %d", exam.QuestionCount)
	}
	if exam.PassPercentage < 0 || exam.PassPercentage > 100 {
		t.Fatalf("pass percentage out of range: %v", exam.PassPercentage)
	}
	if exam.PublishedAt.IsZero() {
		t.Fatalf("published_at missing")
	}
}

func TestExamBySlugReturnsSafeDetail(t *testing.T) {
	handler, _ := newExamsHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exams/cloud-practitioner-practice", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	disallowed := []string{"is_correct", "correct_option", "iscorrect", "explanation", "answer", "is_true", "author_id"}
	for _, term := range disallowed {
		if strings.Contains(strings.ToLower(body), term) {
			t.Fatalf("exam detail leaked %q: %s", term, body)
		}
	}
	var envelope successEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", body)
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PublicExamResponse
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid exam response: %s", string(raw))
	}
	if len(dto.Exam.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(dto.Exam.Questions))
	}
	q := dto.Exam.Questions[0]
	if q.Prompt == "" {
		t.Fatalf("prompt missing")
	}
	if q.Scenario == "" {
		t.Fatalf("scenario missing")
	}
	if len(q.Options) != 3 {
		t.Fatalf("expected 3 options, got %d", len(q.Options))
	}
	for _, option := range q.Options {
		if option.Key == "" || option.Text == "" {
			t.Fatalf("option missing key/text: %#v", option)
		}
	}
	if len(q.References) != 1 || q.References[0].Title != "Lambda Guide" {
		t.Fatalf("references missing: %#v", q.References)
	}
	if dto.Exam.Version <= 0 {
		t.Fatalf("version should be positive: %d", dto.Exam.Version)
	}
	if dto.Exam.Difficulty == "" {
		t.Fatalf("difficulty missing")
	}
}

func TestExamBySlugMissingReturns404(t *testing.T) {
	handler, reader := newExamsHandler()
	reader.errBySlug["missing"] = store.ErrPublishedExamNotFound
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exams/missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "exam_not_found" {
		t.Fatalf("expected exam_not_found error, got %s", w.Body.String())
	}
}

func TestExamBySlugDraftReturns404(t *testing.T) {
	handler, reader := newExamsHandler()
	reader.errBySlug["draft-exam"] = store.ErrPublishedExamNotFound
	reader.examsBySlug["draft-exam"] = exams.PublishedExam{
		ID:    "draft-id",
		Title: "Draft Exam",
		Slug:  "draft-exam",
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exams/draft-exam", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for draft exam, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Draft Exam") {
		t.Fatalf("draft title leaked: %s", w.Body.String())
	}
}

func TestExamBySlugSurfacesInternalErrors(t *testing.T) {
	handler, reader := newExamsHandler()
	reader.errBySlug["oops"] = errors.New("database unavailable")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exams/oops", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d %s", w.Code, w.Body.String())
	}
}

func TestExamsHandlerImplementsExpectedShape(t *testing.T) {
	var _ ExamReader = (*stubExamReader)(nil)
}

func TestPracticeCheckGradesCorrectAnswer(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q1","selected_option_id":"q1-b"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var question attempts.CompletedQuestion
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("invalid feedback: %s", string(raw))
	}
	if !question.IsCorrect {
		t.Fatalf("expected is_correct, got %s", string(raw))
	}
	if len(question.CorrectOptionIDs) != 1 || question.CorrectOptionIDs[0] != "q1-b" {
		t.Fatalf("unexpected correct_option_ids: %v", question.CorrectOptionIDs)
	}
	if len(question.SelectedOptionIDs) != 1 || question.SelectedOptionIDs[0] != "q1-b" {
		t.Fatalf("selected_option_ids not echoed: %v", question.SelectedOptionIDs)
	}
	if question.Explanation == "" {
		t.Fatalf("question explanation missing")
	}
	if question.ID != "q1" {
		t.Fatalf("unexpected question id: %s", question.ID)
	}
	if len(question.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(question.Options))
	}
	if question.Options[1].ID != "q1-b" || question.Options[1].Explanation == "" {
		t.Fatalf("option explanation missing: %#v", question.Options[1])
	}
	t.Logf("graded response: %s", w.Body.String())
}

func TestPracticeCheckResolvesOptionKey(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q1","selected_option_id":"B"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var question attempts.CompletedQuestion
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("invalid feedback: %s", string(raw))
	}
	if !question.IsCorrect {
		t.Fatalf("expected is_correct, got %s", string(raw))
	}
	if len(question.SelectedOptionIDs) != 1 || question.SelectedOptionIDs[0] != "q1-b" {
		t.Fatalf("expected key B to resolve to q1-b, got %v", question.SelectedOptionIDs)
	}
}

func TestPracticeCheckGradesIncorrectAnswer(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q1","selected_option_id":"q1-a"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var question attempts.CompletedQuestion
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("invalid feedback: %s", string(raw))
	}
	if question.IsCorrect {
		t.Fatalf("expected not correct, got %s", string(raw))
	}
	if len(question.CorrectOptionIDs) != 1 || question.CorrectOptionIDs[0] != "q1-b" {
		t.Fatalf("unexpected correct_option_ids: %v", question.CorrectOptionIDs)
	}
	if len(question.SelectedOptionIDs) != 1 || question.SelectedOptionIDs[0] != "q1-a" {
		t.Fatalf("selected_option_ids not echoed: %v", question.SelectedOptionIDs)
	}
}

func TestPracticeCheckGradesMultiAnswer(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q2","selected_option_ids":["q2-c","q2-b"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var question attempts.CompletedQuestion
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("invalid feedback: %s", string(raw))
	}
	if !question.IsCorrect {
		t.Fatalf("expected multi-answer selection to be correct, got %s", string(raw))
	}
	if len(question.CorrectOptionIDs) != 2 || question.CorrectOptionIDs[0] != "q2-b" || question.CorrectOptionIDs[1] != "q2-c" {
		t.Fatalf("unexpected correct_option_ids: %v", question.CorrectOptionIDs)
	}
	if len(question.SelectedOptionIDs) != 2 || question.SelectedOptionIDs[0] != "q2-b" || question.SelectedOptionIDs[1] != "q2-c" {
		t.Fatalf("selected_option_ids not echoed: %v", question.SelectedOptionIDs)
	}

	w, _ = postPractice(t, "cloud-practitioner-practice", `{"question_id":"q2","selected_option_ids":["q2-b"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ = json.Marshal(envelope.Data)
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("invalid feedback: %s", string(raw))
	}
	if question.IsCorrect {
		t.Fatalf("expected partial multi-answer selection to be incorrect, got %s", string(raw))
	}
}

func TestPracticeCheckUnknownSlugReturns404(t *testing.T) {
	w, _ := postPractice(t, "missing", `{"question_id":"q1","selected_option_id":"q1-b"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "exam_not_found" {
		t.Fatalf("expected exam_not_found error, got %s", w.Body.String())
	}
}

func TestPracticeCheckUnknownQuestionReturns404(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q9","selected_option_id":"q1-a"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "question_not_in_exam" {
		t.Fatalf("expected question_not_in_exam error, got %s", w.Body.String())
	}
}

func TestPracticeCheckUnknownOptionReturns422(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q1","selected_option_id":"q9-z"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "option_not_in_question" {
		t.Fatalf("expected option_not_in_question error, got %s", w.Body.String())
	}
	if envelope.Error.Fields["selected_option_id"] != "unknown option" {
		t.Fatalf("expected selected_option_id field, got %s", w.Body.String())
	}
}

func TestPracticeCheckInvalidJSONReturns400(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "invalid_json" {
		t.Fatalf("expected invalid_json error, got %s", w.Body.String())
	}
}

func TestPracticeCheckMissingFieldsReturns400(t *testing.T) {
	w, _ := postPractice(t, "cloud-practitioner-practice", `{"question_id":"q1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request error, got %s", w.Body.String())
	}
	if envelope.Error.Message != "question_id and at least one selected option are required" {
		t.Fatalf("unexpected message: %s", envelope.Error.Message)
	}
	if envelope.Error.Fields["selected_option_ids"] != "at least one selected option is required" {
		t.Fatalf("expected selected_option_ids field, got %s", w.Body.String())
	}
}

func practiceQuestionsFixture() []exams.PracticeQuestion {
	return []exams.PracticeQuestion{
		{
			ExamSlug:   "sample-exam",
			ID:         "q1",
			DomainID:   "domain-cloud",
			Prompt:     "Which service is serverless?",
			Scenario:   "A startup wants to deploy a function without managing servers.",
			Difficulty: exams.Difficulty("beginner"),
			Position:   1,
			Options: []exams.PublishedOption{
				{ID: "q1-a", Key: "A", Text: "EC2", Position: 1},
				{ID: "q1-b", Key: "B", Text: "Lambda", Position: 2},
			},
		},
		{
			ExamSlug:   "sample-exam",
			ID:         "q2",
			DomainID:   "domain-cloud",
			Prompt:     "Which database scales automatically?",
			Difficulty: exams.Difficulty("beginner"),
			Position:   2,
			Options: []exams.PublishedOption{
				{ID: "q2-a", Key: "A", Text: "Oracle", Position: 1},
				{ID: "q2-b", Key: "B", Text: "DynamoDB", Position: 2},
			},
		},
		{
			ExamSlug:   "sample-exam",
			ID:         "q3",
			DomainID:   "domain-cloud",
			Prompt:     "Which control defines the shared responsibility model?",
			Scenario:   "A security team reviews its responsibilities in the cloud.",
			Difficulty: exams.Difficulty("advanced"),
			Position:   3,
			Options: []exams.PublishedOption{
				{ID: "q3-a", Key: "A", Text: "GuardDuty", Position: 1},
				{ID: "q3-b", Key: "B", Text: "IAM", Position: 2},
			},
		},
	}
}

func getPracticeQuestions(t *testing.T, query string, questions []exams.PracticeQuestion) *httptest.ResponseRecorder {
	t.Helper()
	reader := newStubExamReader()
	reader.practiceQuestions = questions
	handler := NewExamsHandler(reader, nil)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/practice/questions"+query, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestPracticeQuestionsReturnsAllByDefault(t *testing.T) {
	w := getPracticeQuestions(t, "", practiceQuestionsFixture())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	disallowed := []string{"is_correct", "correct_option", "explanation", "references", "author_id"}
	for _, term := range disallowed {
		if strings.Contains(strings.ToLower(body), term) {
			t.Fatalf("practice questions leaked %q: %s", term, body)
		}
	}
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PracticeQuestionsDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid practice questions: %s", string(raw))
	}
	if len(dto.Questions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(dto.Questions))
	}
	for i, expected := range []string{"q1", "q2", "q3"} {
		if dto.Questions[i].ID != expected {
			t.Fatalf("expected question %d to be %s, got %s", i, expected, dto.Questions[i].ID)
		}
		if dto.Questions[i].ExamSlug != "sample-exam" {
			t.Fatalf("expected exam_slug sample-exam, got %s", dto.Questions[i].ExamSlug)
		}
	}
}

func TestPracticeQuestionsFiltersByDifficulty(t *testing.T) {
	w := getPracticeQuestions(t, "?difficulty=beginner", practiceQuestionsFixture())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	t.Logf("practice questions response: %s", w.Body.String())
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PracticeQuestionsDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid practice questions: %s", string(raw))
	}
	if len(dto.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d: %s", len(dto.Questions), string(raw))
	}
	if dto.Questions[0].ID != "q1" || dto.Questions[1].ID != "q2" {
		t.Fatalf("unexpected ids: %s %s", dto.Questions[0].ID, dto.Questions[1].ID)
	}
	for _, question := range dto.Questions {
		if question.Difficulty != "beginner" {
			t.Fatalf("unexpected difficulty: %s", question.Difficulty)
		}
		if question.ExamSlug != "sample-exam" {
			t.Fatalf("expected exam_slug, got %s", question.ExamSlug)
		}
		if len(question.Options) == 0 {
			t.Fatalf("expected options, got none: %#v", question)
		}
		for _, option := range question.Options {
			if option.ID == "" || option.Key == "" || option.Text == "" {
				t.Fatalf("option missing id/key/text: %#v", option)
			}
		}
	}
}

func TestPracticeQuestionsInvalidDifficultyReturns400(t *testing.T) {
	w := getPracticeQuestions(t, "?difficulty=weird", practiceQuestionsFixture())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request error, got %s", w.Body.String())
	}
	if envelope.Error.Message != "difficulty must be beginner, intermediate, advanced, or exam_scenarios" {
		t.Fatalf("unexpected message: %s", envelope.Error.Message)
	}
	if envelope.Error.Fields["difficulty"] != "must be beginner, intermediate, advanced, or exam_scenarios" {
		t.Fatalf("expected difficulty field, got %s", w.Body.String())
	}
}

func TestPracticeQuestionsEmptyFixtureReturnsEmptyArray(t *testing.T) {
	w := getPracticeQuestions(t, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"questions":[]`) {
		t.Fatalf("expected empty questions array, got %s", w.Body.String())
	}
}

func TestPracticeQuestionsOmitsEmptyScenario(t *testing.T) {
	fixture := []exams.PracticeQuestion{
		{
			ExamSlug:   "sample-exam",
			ID:         "q1",
			DomainID:   "domain-cloud",
			Prompt:     "Which service is serverless?",
			Difficulty: exams.Difficulty("beginner"),
			Position:   1,
			Options: []exams.PublishedOption{
				{ID: "q1-a", Key: "A", Text: "EC2", Position: 1},
				{ID: "q1-b", Key: "B", Text: "Lambda", Position: 2},
			},
		},
	}
	w := getPracticeQuestions(t, "", fixture)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"scenario"`) {
		t.Fatalf("expected scenario omitted when empty, got %s", w.Body.String())
	}
}
