package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/attempts"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/store"
)

const guestCookieName = "ccarp_guest"

type attemptAnswerRequest struct {
	QuestionID        string   `json:"question_id"`
	SelectedOptionIDs []string `json:"selected_option_ids"`
	SelectedOptionID  string   `json:"selected_option_id"`
}

type AttemptsService interface {
	StartAttempt(ctx context.Context, owner attempts.Owner, examID string) (attempts.Attempt, error)
	SubmitAnswer(ctx context.Context, owner attempts.Owner, attemptID string, input attempts.AnswerInput) (attempts.Attempt, attempts.AnswerFeedback, error)
	SubmitAttempt(ctx context.Context, owner attempts.Owner, attemptID string) (attempts.Attempt, error)
	ListAttempts(ctx context.Context, userID string) ([]attempts.Attempt, error)
	GetAttempt(ctx context.Context, owner attempts.Owner, attemptID string) (attempts.Attempt, error)
	LoadAnswers(ctx context.Context, attemptID string) (map[string]attempts.Answer, error)
}

type AttemptsHandler struct {
	service       AttemptsService
	auth          Authenticator
	secureCookies bool
}

func NewAttemptsHandler(service AttemptsService, auth Authenticator, secureCookies bool) *AttemptsHandler {
	return &AttemptsHandler{service: service, auth: auth, secureCookies: secureCookies}
}

func (h *AttemptsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/exams/{examID}/attempts", OptionalAuth(h.auth, h.startAttempt))
	mux.HandleFunc("POST /api/v1/attempts/{attemptID}/answers", OptionalAuth(h.auth, h.submitAnswer))
	mux.HandleFunc("POST /api/v1/attempts/{attemptID}/submit", OptionalAuth(h.auth, h.submitAttempt))
	mux.HandleFunc("GET /api/v1/attempts", RequireAuth(h.auth, h.listAttempts))
	mux.HandleFunc("GET /api/v1/attempts/{attemptID}", OptionalAuth(h.auth, h.getAttempt))
}

// resolveOwner returns the acting owner for an attempt request. An
// authenticated session takes precedence; otherwise a guest cookie is used.
// When createGuest is true and neither identity exists, a fresh guest token
// is issued and persisted as a cookie.
func (h *AttemptsHandler) resolveOwner(w http.ResponseWriter, r *http.Request, createGuest bool) (attempts.Owner, bool) {
	if user, _, ok := currentAuth(r.Context()); ok {
		return attempts.Owner{UserID: user.ID}, true
	}
	token := guestToken(r)
	if token == "" {
		if !createGuest {
			return attempts.Owner{}, false
		}
		generated, err := newGuestToken()
		if err != nil {
			return attempts.Owner{}, false
		}
		token = generated
		h.setGuestCookie(w, token)
	}
	return attempts.Owner{GuestTokenHash: attempts.HashGuestToken(token)}, true
}

func (h *AttemptsHandler) startAttempt(w http.ResponseWriter, r *http.Request) {
	examID := strings.TrimSpace(r.PathValue("examID"))
	if examID == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "exam id is required", nil)
		return
	}
	owner, ok := h.resolveOwner(w, r, true)
	if !ok {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	attempt, err := h.service.StartAttempt(r.Context(), owner, examID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	WriteData(w, r, http.StatusCreated, activeAttemptResponse(attempt, nil))
}

func (h *AttemptsHandler) submitAnswer(w http.ResponseWriter, r *http.Request) {
	attemptID := strings.TrimSpace(r.PathValue("attemptID"))
	if attemptID == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "attempt id is required", nil)
		return
	}
	var input attemptAnswerRequest
	if err := decodeJSONBody(r, &input); err != nil {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	selection := attempts.CanonicalSelection(input.SelectedOptionIDs, input.SelectedOptionID)
	if input.QuestionID == "" || len(selection) == 0 {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "question_id and at least one selected option are required", map[string]string{
			"question_id":         "question_id is required",
			"selected_option_ids": "at least one selected option is required",
		})
		return
	}
	owner, ok := h.resolveOwner(w, r, false)
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	updated, feedback, err := h.service.SubmitAnswer(r.Context(), owner, attemptID, attempts.AnswerInput{QuestionID: input.QuestionID, SelectedOptionIDs: selection})
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	answers, err := h.service.LoadAnswers(r.Context(), updated.ID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	WriteData(w, r, http.StatusOK, answerResponse{
		Attempt:  activeAttemptResponse(updated, answers),
		Feedback: feedbackQuestionResponse(updated.Snapshot, feedback),
	})
}

func (h *AttemptsHandler) submitAttempt(w http.ResponseWriter, r *http.Request) {
	attemptID := strings.TrimSpace(r.PathValue("attemptID"))
	if attemptID == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "attempt id is required", nil)
		return
	}
	owner, ok := h.resolveOwner(w, r, false)
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	attempt, err := h.service.SubmitAttempt(r.Context(), owner, attemptID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	answers, err := h.service.LoadAnswers(r.Context(), attempt.ID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	WriteData(w, r, http.StatusOK, completedAttemptResponse(attempt, answers))
}

func (h *AttemptsHandler) listAttempts(w http.ResponseWriter, r *http.Request) {
	user, _, ok := currentAuth(r.Context())
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	items, err := h.service.ListAttempts(r.Context(), user.ID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	summaries := make([]attempts.Summary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, attempts.Summary{
			ID:            item.ID,
			ExamID:        item.ExamID,
			ExamSlug:      item.Snapshot.Slug,
			ExamTitle:     item.Snapshot.Title,
			Status:        item.Status,
			QuestionCount: item.QuestionCount,
			CorrectCount:  item.CorrectCount,
			ScorePercent:  item.ScorePercent,
			Passed:        item.Passed,
			StartedAt:     item.StartedAt,
			CompletedAt:   item.CompletedAt,
		})
	}
	WriteData(w, r, http.StatusOK, attemptsListResponse{Attempts: summaries})
}

func (h *AttemptsHandler) getAttempt(w http.ResponseWriter, r *http.Request) {
	attemptID := strings.TrimSpace(r.PathValue("attemptID"))
	if attemptID == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "attempt id is required", nil)
		return
	}
	owner, ok := h.resolveOwner(w, r, false)
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	attempt, err := h.service.GetAttempt(r.Context(), owner, attemptID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	answers, err := h.service.LoadAnswers(r.Context(), attemptID)
	if err != nil {
		writeAttemptError(w, r, err)
		return
	}
	if attempt.Status == attempts.StatusCompleted {
		WriteData(w, r, http.StatusOK, completedAttemptResponse(attempt, answers))
		return
	}
	WriteData(w, r, http.StatusOK, activeAttemptResponse(attempt, answers))
}

func writeAttemptError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, attempts.ErrExamUnavailable), errors.Is(err, store.ErrExamUnavailableForAttempt):
		WriteError(w, r, http.StatusConflict, "exam_unavailable", "Exam is not available for attempts.", nil)
	case errors.Is(err, attempts.ErrQuestionMissing):
		WriteError(w, r, http.StatusUnprocessableEntity, "question_not_in_attempt", "question does not belong to attempt", map[string]string{"question_id": "unknown question"})
	case errors.Is(err, attempts.ErrNoSelection):
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "no option selected", nil)
	case errors.Is(err, attempts.ErrCrossQuestion):
		WriteError(w, r, http.StatusUnprocessableEntity, "option_not_in_question", "selected option does not belong to question", map[string]string{"selected_option_id": "unknown option"})
	case errors.Is(err, attempts.ErrNotInProgress):
		WriteError(w, r, http.StatusConflict, "attempt_not_in_progress", "attempt is not in progress", nil)
	case errors.Is(err, store.ErrAttemptNotFound):
		WriteError(w, r, http.StatusNotFound, "attempt_not_found", "attempt not found", nil)
	default:
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
	}
}

type attemptsListResponse struct {
	Attempts []attempts.Summary `json:"attempts"`
}

type answerResponse struct {
	Attempt  attempts.ActiveAttemptDetail `json:"attempt"`
	Feedback attempts.CompletedQuestion   `json:"feedback"`
}

func activeAttemptResponse(attempt attempts.Attempt, answers map[string]attempts.Answer) attempts.ActiveAttemptDetail {
	questions := make([]attempts.ActiveQuestion, 0, len(attempt.Snapshot.Questions))
	for _, question := range attempt.Snapshot.Questions {
		options := make([]attempts.ActiveOption, 0, len(question.Options))
		correctOptionIDs := attempts.QuestionCorrectOptionIDs(question)
		for _, option := range question.Options {
			options = append(options, attempts.ActiveOption{
				ID:       option.ID,
				Key:      option.Key,
				Text:     option.Text,
				Position: option.Position,
			})
		}
		references := make([]exams.SnapshotReference, 0, len(question.References))
		references = append(references, question.References...)
		var selectedIDs []string
		var isCorrect *bool
		if answers != nil {
			if ans, ok := answers[question.ID]; ok {
				selectedIDs = ans.SelectedOptionIDs
				isCorrect = ans.IsCorrect
			}
		}
		var revealedCorrectIDs []string
		if len(selectedIDs) > 0 {
			revealedCorrectIDs = correctOptionIDs
		}
		questions = append(questions, attempts.ActiveQuestion{
			ID:                question.ID,
			Position:          question.Position,
			DomainID:          question.DomainID,
			Prompt:            question.Prompt,
			Scenario:          question.Scenario,
			Difficulty:        question.Difficulty,
			Options:           options,
			References:        references,
			SelectedOptionIDs: selectedIDs,
			IsCorrect:         isCorrect,
			CorrectOptionIDs:  revealedCorrectIDs,
		})
	}
	return attempts.ActiveAttemptDetail{
		ID:            attempt.ID,
		Status:        attempt.Status,
		Exam:          activeExamMeta(attempt.Snapshot),
		QuestionCount: attempt.QuestionCount,
		StartedAt:     attempt.StartedAt,
		UpdatedAt:     attempt.UpdatedAt,
		Questions:     questions,
	}
}

func feedbackQuestionResponse(snapshot exams.Snapshot, feedback attempts.AnswerFeedback) attempts.CompletedQuestion {
	for _, question := range snapshot.Questions {
		if question.ID != feedback.QuestionID {
			continue
		}
		options := make([]attempts.CompletedOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, attempts.CompletedOption{
				ID:          option.ID,
				Key:         option.Key,
				Text:        option.Text,
				Explanation: option.Explanation,
				Position:    option.Position,
			})
		}
		references := make([]exams.SnapshotReference, 0, len(question.References))
		references = append(references, question.References...)
		return attempts.CompletedQuestion{
			ID:                question.ID,
			Position:          question.Position,
			DomainID:          question.DomainID,
			Prompt:            question.Prompt,
			Scenario:          question.Scenario,
			Difficulty:        question.Difficulty,
			Explanation:       feedback.Explanation,
			Options:           options,
			References:        references,
			SelectedOptionIDs: feedback.SelectedOptionIDs,
			CorrectOptionIDs:  feedback.CorrectOptionIDs,
			IsCorrect:         feedback.IsCorrect,
		}
	}
	return attempts.CompletedQuestion{}
}

func completedAttemptResponse(attempt attempts.Attempt, answers map[string]attempts.Answer) attempts.CompletedAttemptDetail {
	correct := 0
	if attempt.CorrectCount != nil {
		correct = *attempt.CorrectCount
	}
	score := 0.0
	if attempt.ScorePercent != nil {
		score = *attempt.ScorePercent
	}
	passed := false
	if attempt.Passed != nil {
		passed = *attempt.Passed
	}
	completed := time.Time{}
	if attempt.CompletedAt != nil {
		completed = *attempt.CompletedAt
	}
	questions := make([]attempts.CompletedQuestion, 0, len(attempt.Snapshot.Questions))
	for _, question := range attempt.Snapshot.Questions {
		correctOptionIDs := attempts.QuestionCorrectOptionIDs(question)
		options := make([]attempts.CompletedOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, attempts.CompletedOption{
				ID:          option.ID,
				Key:         option.Key,
				Text:        option.Text,
				Explanation: option.Explanation,
				Position:    option.Position,
			})
		}
		var selectedIDs []string
		isCorrect := false
		if answers != nil {
			if ans, ok := answers[question.ID]; ok {
				selectedIDs = ans.SelectedOptionIDs
				if ans.IsCorrect != nil {
					isCorrect = *ans.IsCorrect
				} else {
					isCorrect = attempts.SelectionsAreCorrect(question, selectedIDs)
				}
			}
		}
		references := make([]exams.SnapshotReference, 0, len(question.References))
		references = append(references, question.References...)
		questions = append(questions, attempts.CompletedQuestion{
			ID:                question.ID,
			Position:          question.Position,
			DomainID:          question.DomainID,
			Prompt:            question.Prompt,
			Scenario:          question.Scenario,
			Difficulty:        question.Difficulty,
			Explanation:       question.Explanation,
			Options:           options,
			References:        references,
			SelectedOptionIDs: selectedIDs,
			CorrectOptionIDs:  correctOptionIDs,
			IsCorrect:         isCorrect,
		})
	}
	return attempts.CompletedAttemptDetail{
		ID:             attempt.ID,
		Status:         attempt.Status,
		Exam:           activeExamMeta(attempt.Snapshot),
		QuestionCount:  attempt.QuestionCount,
		CorrectCount:   correct,
		ScorePercent:   score,
		Passed:         passed,
		PassPercentage: attempt.Snapshot.PassPercentage,
		StartedAt:      attempt.StartedAt,
		CompletedAt:    completed,
		Questions:      questions,
	}
}

func activeExamMeta(snapshot exams.Snapshot) attempts.ActiveExamMeta {
	return attempts.ActiveExamMeta{
		ID:               snapshot.ExamID,
		Slug:             snapshot.Slug,
		Title:            snapshot.Title,
		Description:      snapshot.Description,
		Difficulty:       snapshot.Difficulty,
		TimeLimitMinutes: snapshot.TimeLimitMinutes,
		PassPercentage:   snapshot.PassPercentage,
		Version:          snapshot.Version,
	}
}

func guestToken(r *http.Request) string {
	cookie, err := r.Cookie(guestCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func newGuestToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (h *AttemptsHandler) setGuestCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     guestCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int((180 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}
