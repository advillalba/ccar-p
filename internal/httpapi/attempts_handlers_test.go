package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/attempts"
	"github.com/ccar-p/study-platform/internal/auth"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/store"
)

type stubAttemptsService struct {
	mu sync.Mutex

	attempts map[string]attempts.Attempt
	answers  map[string]map[string]attempts.Answer

	startErr   error
	answerErr  error
	submitErr  error
	listErr    error
	getErr     error
	loadAnsErr error

	startCalls int
}

func newStubAttemptsService() *stubAttemptsService {
	return &stubAttemptsService{attempts: map[string]attempts.Attempt{}, answers: map[string]map[string]attempts.Answer{}}
}

func snapshot() exams.Snapshot {
	return exams.Snapshot{
		ExamID:           "exam-1",
		Version:          2,
		Title:            "Sample Exam",
		Slug:             "sample-exam",
		Description:      "description",
		Difficulty:       exams.Difficulty("easy"),
		TimeLimitMinutes: 30,
		PassPercentage:   60,
		QuestionIDs:      []string{"q1", "q2"},
		Questions: []exams.SnapshotQuestion{
			{ID: "q1", DomainID: "dom-1", Prompt: "P1", Scenario: "S1", Explanation: "Explain 1", Difficulty: exams.Difficulty("easy"), Position: 1,
				Options: []exams.SnapshotOption{
					{ID: "q1-a", Key: "A", Text: "a text", Explanation: "Why A", Position: 1},
					{ID: "q1-b", Key: "B", Text: "b text", Explanation: "Why B", Position: 2, IsCorrect: true},
				},
				References: []exams.SnapshotReference{{Title: "Ref 1", URL: "https://example.com", Position: 1}},
			},
			{ID: "q2", DomainID: "dom-1", Prompt: "P2", Scenario: "S2", Explanation: "Explain 2", Difficulty: exams.Difficulty("easy"), Position: 2,
				Options: []exams.SnapshotOption{
					{ID: "q2-a", Key: "A", Text: "a text", Explanation: "Why A", Position: 1, IsCorrect: true},
					{ID: "q2-b", Key: "B", Text: "b text", Explanation: "Why B", Position: 2},
				},
			},
		},
	}
}

func ownerMatches(a attempts.Attempt, o attempts.Owner) bool {
	if o.UserID != "" {
		return a.UserID == o.UserID
	}
	return bytes.Equal(a.GuestTokenHash, o.GuestTokenHash)
}

func (s *stubAttemptsService) StartAttempt(_ context.Context, owner attempts.Owner, examID string) (attempts.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return attempts.Attempt{}, s.startErr
	}
	s.startCalls++
	now := time.Now().UTC()
	attempt := attempts.Attempt{ID: "attempt-1", UserID: owner.UserID, GuestTokenHash: owner.GuestTokenHash, ExamID: examID, ExamVersionID: "ev-1", Status: attempts.StatusInProgress, QuestionCount: 2, Snapshot: snapshot(), StartedAt: now, UpdatedAt: now}
	s.attempts[attempt.ID] = attempt
	s.answers[attempt.ID] = map[string]attempts.Answer{}
	return attempt, nil
}

func (s *stubAttemptsService) SubmitAnswer(_ context.Context, owner attempts.Owner, attemptID string, input attempts.AnswerInput) (attempts.Attempt, attempts.AnswerFeedback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.answerErr != nil {
		return attempts.Attempt{}, attempts.AnswerFeedback{}, s.answerErr
	}
	attempt, ok := s.attempts[attemptID]
	if !ok || !ownerMatches(attempt, owner) {
		return attempts.Attempt{}, attempts.AnswerFeedback{}, store.ErrAttemptNotFound
	}
	bag, ok := s.answers[attemptID]
	if !ok {
		bag = map[string]attempts.Answer{}
		s.answers[attemptID] = bag
	}
	selection := attempts.CanonicalSelection(input.SelectedOptionIDs, "")
	feedback := attempts.AnswerFeedback{QuestionID: input.QuestionID, SelectedOptionIDs: selection}
	for _, q := range attempt.Snapshot.Questions {
		if q.ID != input.QuestionID {
			continue
		}
		feedback.Explanation = q.Explanation
		feedback.CorrectOptionIDs = attempts.QuestionCorrectOptionIDs(q)
		feedback.IsCorrect = attempts.SelectionsAreCorrect(q, selection)
	}
	isCorrect := feedback.IsCorrect
	bag[input.QuestionID] = attempts.Answer{AttemptID: attemptID, QuestionID: input.QuestionID, SelectedOptionIDs: selection, IsCorrect: &isCorrect, AnsweredAt: time.Now().UTC()}
	return attempt, feedback, nil
}

func (s *stubAttemptsService) SubmitAttempt(_ context.Context, owner attempts.Owner, attemptID string) (attempts.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.submitErr != nil {
		return attempts.Attempt{}, s.submitErr
	}
	attempt, ok := s.attempts[attemptID]
	if !ok || !ownerMatches(attempt, owner) {
		return attempts.Attempt{}, store.ErrAttemptNotFound
	}
	correct := 1
	score := 50.0
	passed := false
	now := time.Now().UTC()
	attempt.Status = attempts.StatusCompleted
	attempt.CorrectCount = &correct
	attempt.ScorePercent = &score
	attempt.Passed = &passed
	attempt.CompletedAt = &now
	s.attempts[attemptID] = attempt
	return attempt, nil
}

func (s *stubAttemptsService) ListAttempts(_ context.Context, userID string) ([]attempts.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []attempts.Attempt{}
	for _, a := range s.attempts {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *stubAttemptsService) GetAttempt(_ context.Context, owner attempts.Owner, attemptID string) (attempts.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return attempts.Attempt{}, s.getErr
	}
	attempt, ok := s.attempts[attemptID]
	if !ok || !ownerMatches(attempt, owner) {
		return attempts.Attempt{}, store.ErrAttemptNotFound
	}
	return attempt, nil
}

func (s *stubAttemptsService) LoadAnswers(_ context.Context, attemptID string) (map[string]attempts.Answer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadAnsErr != nil {
		return nil, s.loadAnsErr
	}
	if bag, ok := s.answers[attemptID]; ok {
		copy := map[string]attempts.Answer{}
		for k, v := range bag {
			copy[k] = v
		}
		return copy, nil
	}
	return map[string]attempts.Answer{}, nil
}

type attemptsAuthenticator struct {
	user auth.User
	err  error
}

func (s attemptsAuthenticator) Authenticate(_ context.Context, _ string) (auth.User, auth.Session, error) {
	if s.err != nil {
		return auth.User{}, auth.Session{}, s.err
	}
	return s.user, auth.Session{ID: "s-1", UserID: s.user.ID}, nil
}

func newAttemptsRouter(t *testing.T) (*http.ServeMux, *stubAttemptsService) {
	t.Helper()
	svc := newStubAttemptsService()
	authn := attemptsAuthenticator{user: auth.User{ID: "user-1", Email: "user@example.com", Role: auth.RoleStudent, IsActive: true}}
	handler := NewAttemptsHandler(svc, authn, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux, svc
}

func doRequest(t *testing.T, mux http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookieName+"=test-token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeEnvelope(t *testing.T, body *bytes.Buffer) (map[string]any, map[string]any) {
	t.Helper()
	var env struct {
		Data  map[string]any `json:"data"`
		ReqID string         `json:"request_id"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Data, env.Error
}

func TestAttemptsHandlerStartSuccess(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/exams/exam-1/attempts", nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	if data["status"] != "in_progress" {
		t.Fatalf("expected status in_progress, got %v", data["status"])
	}
	if data["id"] != "attempt-1" {
		t.Fatalf("expected id attempt-1, got %v", data["id"])
	}
	if _, ok := data["questions"]; !ok {
		t.Fatalf("expected questions in payload")
	}
	qRaw, ok := data["questions"].([]any)
	if !ok || len(qRaw) == 0 {
		t.Fatalf("expected at least one question, got %v", data["questions"])
	}
	first, ok := qRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected question object")
	}
	if _, has := first["explanation"]; has {
		t.Fatalf("active DTO must not include explanation")
	}
	if _, has := first["correct_option_id"]; has {
		t.Fatalf("unanswered question must not reveal the correct option")
	}
	options, ok := first["options"].([]any)
	if !ok || len(options) == 0 {
		t.Fatalf("expected options")
	}
	for _, opt := range options {
		item := opt.(map[string]any)
		if _, has := item["is_correct"]; has {
			t.Fatalf("active DTO must not include is_correct")
		}
		if _, has := item["explanation"]; has {
			t.Fatalf("active DTO must not include option explanation")
		}
	}
	if svc.startCalls != 1 {
		t.Fatalf("expected start called once")
	}
}

func TestAttemptsHandlerStartExamUnavailable(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	svc.startErr = attempts.ErrExamUnavailable
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/exams/exam-1/attempts", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttemptsHandlerSubmitAnswerSuccess(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	payload := attemptAnswerRequest{QuestionID: "q1", SelectedOptionIDs: []string{"q1-b"}}
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/attempts/attempt-1/answers", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	attemptData, ok := data["attempt"].(map[string]any)
	if !ok {
		t.Fatalf("expected attempt envelope, got %v", data)
	}
	questions := attemptData["questions"].([]any)
	first := questions[0].(map[string]any)
	if ids, _ := first["selected_option_ids"].([]any); len(ids) != 1 || ids[0] != "q1-b" {
		t.Fatalf("expected selected option set, got %v", first["selected_option_ids"])
	}
	feedback, ok := data["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("expected feedback, got %v", data)
	}
	if feedback["is_correct"] != true {
		t.Fatalf("expected is_correct true, got %v", feedback["is_correct"])
	}
	if ids, _ := feedback["correct_option_ids"].([]any); len(ids) != 1 || ids[0] != "q1-b" {
		t.Fatalf("expected correct_option_ids [q1-b], got %v", feedback["correct_option_ids"])
	}
	if feedback["explanation"] != "Explain 1" {
		t.Fatalf("expected explanation, got %v", feedback["explanation"])
	}
}

func TestAttemptsHandlerSubmitAnswerIncorrect(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	payload := attemptAnswerRequest{QuestionID: "q1", SelectedOptionIDs: []string{"q1-a"}}
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/attempts/attempt-1/answers", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	feedback, ok := data["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("expected feedback, got %v", data)
	}
	if feedback["is_correct"] != false {
		t.Fatalf("expected is_correct false, got %v", feedback["is_correct"])
	}
	if ids, _ := feedback["correct_option_ids"].([]any); len(ids) != 1 || ids[0] != "q1-b" {
		t.Fatalf("expected correct_option_ids [q1-b], got %v", feedback["correct_option_ids"])
	}
}

func TestAttemptsHandlerSubmitAnswerInvalidPayload(t *testing.T) {
	mux, _ := newAttemptsRouter(t)
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/attempts/attempt-1/answers", map[string]string{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttemptsHandlerSubmitAnswerWrongOption(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.answerErr = attempts.ErrCrossQuestion
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/attempts/attempt-1/answers", attemptAnswerRequest{QuestionID: "q1", SelectedOptionIDs: []string{"q2-a"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttemptsHandlerSubmitAttemptCompleted(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.answers["attempt-1"] = map[string]attempts.Answer{"q1": {AttemptID: "attempt-1", QuestionID: "q1", SelectedOptionIDs: []string{"q1-b"}}}
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/attempts/attempt-1/submit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	if data["status"] != "completed" {
		t.Fatalf("expected status completed, got %v", data["status"])
	}
	questions := data["questions"].([]any)
	first := questions[0].(map[string]any)
	if ids, _ := first["correct_option_ids"].([]any); len(ids) != 1 || ids[0] != "q1-b" {
		t.Fatalf("expected correct_option_ids [q1-b], got %v", first["correct_option_ids"])
	}
	if first["explanation"] == nil || first["explanation"] == "" {
		t.Fatalf("expected explanation populated")
	}
	options := first["options"].([]any)
	if options[0].(map[string]any)["explanation"] != "Why A" || options[1].(map[string]any)["explanation"] != "Why B" {
		t.Fatalf("expected completed option explanations, got %v", options)
	}
}

func TestAttemptsHandlerListAttempts(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/attempts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	items := data["attempts"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(items))
	}
}

func TestAttemptsHandlerGetAttemptActiveRevealsAnsweredResult(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.answers["attempt-1"] = map[string]attempts.Answer{"q1": {AttemptID: "attempt-1", QuestionID: "q1", SelectedOptionIDs: []string{"q1-b"}}}
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/attempts/attempt-1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	if data["status"] != "in_progress" {
		t.Fatalf("expected in_progress, got %v", data["status"])
	}
	questions := data["questions"].([]any)
	first := questions[0].(map[string]any)
	if ids, _ := first["selected_option_ids"].([]any); len(ids) != 1 || ids[0] != "q1-b" {
		t.Fatalf("expected selected option to be exposed for active, got %v", first["selected_option_ids"])
	}
	if ids, _ := first["correct_option_ids"].([]any); len(ids) != 1 || ids[0] != "q1-b" {
		t.Fatalf("expected answered question to reveal correct_option_ids, got %v", first["correct_option_ids"])
	}
	if _, has := first["explanation"]; has {
		t.Fatalf("active DTO must not include explanation")
	}
	second := questions[1].(map[string]any)
	if _, has := second["correct_option_ids"]; has {
		t.Fatalf("unanswered question must not reveal correct_option_ids")
	}
}

func TestAttemptsHandlerGetAttemptNotFound(t *testing.T) {
	mux, _ := newAttemptsRouter(t)
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/attempts/missing", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttemptsHandlerListRequiresAuth(t *testing.T) {
	svc := newStubAttemptsService()
	handler := NewAttemptsHandler(svc, attemptsAuthenticator{err: errors.New("invalid session")}, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/attempts", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttemptsHandlerGuestStartAndGet(t *testing.T) {
	svc := newStubAttemptsService()
	// No session: the authenticator rejects any token, so only a guest
	// cookie can identify the owner.
	handler := NewAttemptsHandler(svc, attemptsAuthenticator{err: errors.New("invalid session")}, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/exams/exam-1/attempts", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected guest start 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	cookie := rr.Result().Cookies()
	var guestCookie *http.Cookie
	for _, c := range cookie {
		if c.Name == guestCookieName {
			guestCookie = c
		}
	}
	if guestCookie == nil || guestCookie.Value == "" {
		t.Fatalf("expected guest cookie to be issued")
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/attempts/attempt-1", nil)
	get.AddCookie(guestCookie)
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, get)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected guest get 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestAttemptsHandlerGetWithoutOwnerUnauthorized(t *testing.T) {
	svc := newStubAttemptsService()
	handler := NewAttemptsHandler(svc, attemptsAuthenticator{err: errors.New("invalid session")}, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/attempts/attempt-1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttemptsHandlerEmptyPath(t *testing.T) {
	mux, _ := newAttemptsRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/attempts/%20%20", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestAttemptsHandlerGetCompleted(t *testing.T) {
	mux, svc := newAttemptsRouter(t)
	if _, err := svc.StartAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "exam-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.SubmitAttempt(context.Background(), attempts.Owner{UserID: "user-1"}, "attempt-1"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/attempts/attempt-1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data, _ := decodeEnvelope(t, rr.Body)
	if data["status"] != "completed" {
		t.Fatalf("expected status completed, got %v", data["status"])
	}
}

func TestAttemptsHandlerMissingAttemptID(t *testing.T) {
	mux, _ := newAttemptsRouter(t)
	rr := doRequest(t, mux, http.MethodPost, "/api/v1/exams//attempts", nil)
	// Go 1.25+ ServeMux preserves the method on redirects, returning 307
	// instead of 301 for this request.
	if rr.Code != http.StatusMovedPermanently && rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 301/307 redirect from mux, got %d", rr.Code)
	}
}

func TestAttemptsHandlerPayloadBody(t *testing.T) {
	mux, _ := newAttemptsRouter(t)
	body := strings.NewReader("not json")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/attempts/attempt-1/answers", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookieName+"=test-token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}
