package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/attempts"
	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/store"
)

type PublicExamSummaryDTO struct {
	ID               string         `json:"id"`
	Slug             string         `json:"slug"`
	Title            string         `json:"title"`
	Description      string         `json:"description"`
	Difficulty       string         `json:"difficulty"`
	TimeLimitMinutes int            `json:"time_limit_minutes"`
	PassPercentage   float64        `json:"pass_percentage"`
	QuestionCount    int            `json:"question_count"`
	Domains          []PublicDomain `json:"domains"`
	PublishedAt      time.Time      `json:"published_at"`
}

type PublicExamDetailDTO struct {
	ID               string           `json:"id"`
	Slug             string           `json:"slug"`
	Title            string           `json:"title"`
	Description      string           `json:"description"`
	Difficulty       string           `json:"difficulty"`
	TimeLimitMinutes int              `json:"time_limit_minutes"`
	PassPercentage   float64          `json:"pass_percentage"`
	Version          int              `json:"version"`
	PublishedAt      time.Time        `json:"published_at"`
	Questions        []PublicQuestion `json:"questions"`
}

type PublicQuestion struct {
	ID         string          `json:"id"`
	DomainID   string          `json:"domain_id"`
	Prompt     string          `json:"prompt"`
	Scenario   string          `json:"scenario"`
	Difficulty string          `json:"difficulty"`
	Position   int             `json:"position"`
	Options    []PublicOption  `json:"options"`
	References []PublicExamRef `json:"references"`
}

type PublicOption struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type PublicExamRef struct {
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	Citation string `json:"citation,omitempty"`
	Position int    `json:"position"`
}

type PublicExamListDTO struct {
	Exams []PublicExamSummaryDTO `json:"exams"`
}

type PublicExamResponse struct {
	Exam PublicExamDetailDTO `json:"exam"`
}

type ExamReader interface {
	ListPublishedExams(ctx context.Context) ([]exams.PublishedExamSummary, error)
	PublishedExamBySlug(ctx context.Context, slug string) (exams.PublishedExam, error)
	PublishedSnapshotBySlug(ctx context.Context, slug string) (exams.Snapshot, error)
	PublishedPracticeQuestions(ctx context.Context, difficulty string, domainIDs []string) ([]exams.PracticeQuestion, error)
}

type DomainReader interface {
	ActiveDomains(ctx context.Context) ([]content.Domain, error)
}

type PracticeStore interface {
	UpsertAnswer(ctx context.Context, userID, questionID string, selected []string, isCorrect bool) error
	ListAnswers(ctx context.Context, userID string) (map[string]store.PracticeAnswer, error)
	DeleteAll(ctx context.Context, userID string) error
}

type ExamsHandler struct {
	reader   ExamReader
	domains  DomainReader
	practice PracticeStore
	auth     Authenticator
}

func NewExamsHandler(reader ExamReader, domains DomainReader) *ExamsHandler {
	return &ExamsHandler{reader: reader, domains: domains}
}

func NewExamsHandlerWithPractice(reader ExamReader, domains DomainReader, practice PracticeStore, auth Authenticator) *ExamsHandler {
	return &ExamsHandler{reader: reader, domains: domains, practice: practice, auth: auth}
}

func (h *ExamsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/exams", h.listExams)
	mux.HandleFunc("GET /api/v1/exams/{slug}", h.getExamBySlug)
	if h.auth != nil {
		mux.HandleFunc("POST /api/v1/exams/{slug}/practice", OptionalAuth(h.auth, h.practiceCheck))
	} else {
		mux.HandleFunc("POST /api/v1/exams/{slug}/practice", h.practiceCheck)
	}
	mux.HandleFunc("GET /api/v1/practice/questions", h.practiceQuestions)
	if h.auth != nil && h.practice != nil {
		mux.HandleFunc("GET /api/v1/practice/progress", RequireAuth(h.auth, h.practiceProgress))
		mux.HandleFunc("DELETE /api/v1/practice/progress", RequireAuth(h.auth, h.practiceReset))
	}
}

func (h *ExamsHandler) listExams(w http.ResponseWriter, r *http.Request) {
	items, err := h.reader.ListPublishedExams(r.Context())
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	domainsByID := map[string]content.Domain{}
	if h.domains != nil {
		domains, err := h.domains.ActiveDomains(r.Context())
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
			return
		}
		for _, domain := range domains {
			domainsByID[domain.ID] = domain
		}
	}
	out := PublicExamListDTO{Exams: make([]PublicExamSummaryDTO, 0, len(items))}
	for _, exam := range items {
		summary := PublicExamSummaryDTO{
			ID:               exam.ID,
			Slug:             exam.Slug,
			Title:            exam.Title,
			Description:      exam.Description,
			Difficulty:       string(exam.Difficulty),
			TimeLimitMinutes: exam.TimeLimitMinutes,
			PassPercentage:   exam.PassPercentage,
			QuestionCount:    exam.QuestionCount,
			PublishedAt:      exam.PublishedAt,
			Domains:          []PublicDomain{},
		}
		for _, domainID := range exam.DomainIDs {
			if domain, ok := domainsByID[domainID]; ok {
				summary.Domains = append(summary.Domains, PublicDomain{ID: domain.ID, Slug: domain.Slug, Name: domain.Name, Weight: domain.Weight, SortOrder: domain.SortOrder})
			}
		}
		out.Exams = append(out.Exams, summary)
	}
	WriteData(w, r, http.StatusOK, out)
}

func (h *ExamsHandler) getExamBySlug(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_slug", "exam slug is required", nil)
		return
	}
	exam, err := h.reader.PublishedExamBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrPublishedExamNotFound) {
			WriteError(w, r, http.StatusNotFound, "exam_not_found", "exam not found", nil)
			return
		}
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusOK, PublicExamResponse{Exam: publicExam(exam)})
}

func publicExam(exam exams.PublishedExam) PublicExamDetailDTO {
	detail := PublicExamDetailDTO{
		ID:               exam.ID,
		Slug:             exam.Slug,
		Title:            exam.Title,
		Description:      exam.Description,
		Difficulty:       string(exam.Difficulty),
		TimeLimitMinutes: exam.TimeLimitMinutes,
		PassPercentage:   exam.PassPercentage,
		Version:          exam.Version,
		PublishedAt:      exam.PublishedAt,
		Questions:        make([]PublicQuestion, 0, len(exam.Questions)),
	}
	for _, q := range exam.Questions {
		detail.Questions = append(detail.Questions, publicQuestion(q))
	}
	return detail
}

func publicQuestion(q exams.PublishedQuestion) PublicQuestion {
	out := PublicQuestion{
		ID:         q.ID,
		DomainID:   q.DomainID,
		Prompt:     q.Prompt,
		Scenario:   q.Scenario,
		Difficulty: string(q.Difficulty),
		Position:   q.Position,
		Options:    make([]PublicOption, 0, len(q.Options)),
		References: make([]PublicExamRef, 0, len(q.References)),
	}
	for _, option := range q.Options {
		out.Options = append(out.Options, PublicOption{ID: option.ID, Key: option.Key, Text: option.Text, Position: option.Position})
	}
	for _, ref := range q.References {
		out.References = append(out.References, PublicExamRef{Title: ref.Title, URL: ref.URL, Citation: ref.Citation, Position: ref.Position})
	}
	return out
}

func (h *ExamsHandler) practiceCheck(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_slug", "exam slug is required", nil)
		return
	}
	var input practiceCheckRequest
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
	snapshot, err := h.reader.PublishedSnapshotBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrPublishedExamNotFound) {
			WriteError(w, r, http.StatusNotFound, "exam_not_found", "exam not found", nil)
			return
		}
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	question, err := practiceFeedback(snapshot, input.QuestionID, selection)
	if err != nil {
		switch {
		case errors.Is(err, practiceQuestionMissing):
			WriteError(w, r, http.StatusNotFound, "question_not_in_exam", "question does not belong to this exam", nil)
		case errors.Is(err, practiceOptionMissing):
			WriteError(w, r, http.StatusUnprocessableEntity, "option_not_in_question", "selected option does not belong to question", map[string]string{"selected_option_id": "unknown option"})
		default:
			WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		}
		return
	}
	// Persist practice answer for authenticated users
	if h.practice != nil {
		if user, _, ok := currentAuth(r.Context()); ok {
			// Use resolved IDs from feedback to ensure we store canonical option IDs
			selectedIDs := question.SelectedOptionIDs
			if len(selectedIDs) == 0 {
				selectedIDs = selection
			}
			_ = h.practice.UpsertAnswer(r.Context(), user.ID, input.QuestionID, selectedIDs, question.IsCorrect)
		} else {
			// For guests, set a cookie with practice progress (frontend also handles, but set for SSR)
			h.setPracticeCookie(w, r, input.QuestionID, question.IsCorrect, question.SelectedOptionIDs)
		}
	}
	WriteData(w, r, http.StatusOK, question)
}

func (h *ExamsHandler) practiceProgress(w http.ResponseWriter, r *http.Request) {
	user, _, ok := currentAuth(r.Context())
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	if h.practice == nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	answers, err := h.practice.ListAnswers(r.Context(), user.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	out := PracticeProgressDTO{Answers: make([]PracticeProgressAnswer, 0, len(answers))}
	for _, a := range answers {
		out.Answers = append(out.Answers, PracticeProgressAnswer{
			QuestionID:        a.QuestionID,
			SelectedOptionIDs: a.SelectedOptionIDs,
			IsCorrect:         a.IsCorrect,
			AnsweredAt:        a.AnsweredAt,
		})
	}
	WriteData(w, r, http.StatusOK, out)
}

func (h *ExamsHandler) practiceReset(w http.ResponseWriter, r *http.Request) {
	user, _, ok := currentAuth(r.Context())
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	if h.practice == nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	if err := h.practice.DeleteAll(r.Context(), user.ID); err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	// Clear guest practice cookie as well (in case user had guest progress)
	http.SetCookie(w, &http.Cookie{Name: practiceCookieName, Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode})
	WriteData(w, r, http.StatusOK, map[string]string{"status": "reset"})
}

const practiceCookieName = "ccarp_practice"

func (h *ExamsHandler) setPracticeCookie(w http.ResponseWriter, r *http.Request, questionID string, isCorrect bool, selected []string) {
	existing := map[string]any{}
	if cookie, err := r.Cookie(practiceCookieName); err == nil && cookie.Value != "" {
		decoded, _ := decodePracticeCookie(cookie.Value)
		if decoded != nil {
			existing = decoded
		}
	}
	existing[questionID] = map[string]any{"is_correct": isCorrect, "selected": selected}
	encoded := encodePracticeCookie(existing)
	http.SetCookie(w, &http.Cookie{
		Name:     practiceCookieName,
		Value:    encoded,
		Path:     "/",
		MaxAge:   int((180 * 24 * time.Hour).Seconds()),
		SameSite: http.SameSiteLaxMode,
	})
}

func encodePracticeCookie(data map[string]any) string {
	b, _ := json.Marshal(data)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePracticeCookie(value string) (map[string]any, error) {
	// Try RawURLEncoding first (backend), then StdEncoding, then raw JSON
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(value); err == nil {
			var out map[string]any
			if err := json.Unmarshal(b, &out); err == nil {
				return out, nil
			}
		}
	}
	// Try with padding fix for URL encoding
	padded := value
	if m := len(padded) % 4; m != 0 {
		padded += strings.Repeat("=", 4-m)
	}
	padded = strings.ReplaceAll(strings.ReplaceAll(padded, "-", "+"), "_", "/")
	if b, err := base64.StdEncoding.DecodeString(padded); err == nil {
		var out map[string]any
		if err := json.Unmarshal(b, &out); err == nil {
			return out, nil
		}
	}
	// Last try: value is already JSON
	var out map[string]any
	if err := json.Unmarshal([]byte(value), &out); err == nil {
		return out, nil
	}
	return nil, errors.New("invalid practice cookie")
}

func (h *ExamsHandler) practiceQuestions(w http.ResponseWriter, r *http.Request) {
	difficulty := strings.TrimSpace(r.URL.Query().Get("difficulty"))
	if difficulty != "" && difficulty != "beginner" && difficulty != "intermediate" && difficulty != "advanced" && difficulty != "exam_scenarios" {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "difficulty must be beginner, intermediate, advanced, or exam_scenarios", map[string]string{
			"difficulty": "must be beginner, intermediate, advanced, or exam_scenarios",
		})
		return
	}
	domainIDs := parseDomainFilter(r, h.domains)
	items, err := h.reader.PublishedPracticeQuestions(r.Context(), difficulty, domainIDs)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	out := PracticeQuestionsDTO{Questions: make([]PracticeQuestionDTO, 0, len(items))}
	for _, item := range items {
		question := PracticeQuestionDTO{
			ID:         item.ID,
			DomainID:   item.DomainID,
			ExamSlug:   item.ExamSlug,
			Prompt:     item.Prompt,
			Scenario:   item.Scenario,
			Difficulty: string(item.Difficulty),
			Position:   item.Position,
			Options:    make([]PublicOption, 0, len(item.Options)),
		}
		for _, option := range item.Options {
			question.Options = append(question.Options, PublicOption{ID: option.ID, Key: option.Key, Text: option.Text, Position: option.Position})
		}
		out.Questions = append(out.Questions, question)
	}
	WriteData(w, r, http.StatusOK, out)
}

func parseDomainFilter(r *http.Request, reader DomainReader) []string {
	query := r.URL.Query()
	rawValues := []string{}
	for _, key := range []string{"domains", "domain", "domain_id", "domain_ids", "domainIds"} {
		for _, v := range query[key] {
			if v == "" {
				continue
			}
			for _, part := range strings.Split(v, ",") {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					rawValues = append(rawValues, trimmed)
				}
			}
		}
	}
	if len(rawValues) == 0 {
		return nil
	}
	// Deduplicate
	seen := map[string]struct{}{}
	unique := []string{}
	for _, v := range rawValues {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			unique = append(unique, v)
		}
	}
	if reader == nil {
		return unique
	}
	domains, err := reader.ActiveDomains(r.Context())
	if err != nil {
		return unique
	}
	byID := map[string]struct{}{}
	bySlug := map[string]string{}
	for _, d := range domains {
		byID[d.ID] = struct{}{}
		bySlug[strings.ToLower(d.Slug)] = d.ID
		bySlug[strings.ToLower(d.ID)] = d.ID
	}
	resolved := []string{}
	seenResolved := map[string]struct{}{}
	for _, token := range unique {
		lower := strings.ToLower(token)
		var id string
		if mapped, ok := bySlug[lower]; ok {
			id = mapped
		} else if _, ok := byID[token]; ok {
			id = token
		} else {
			// treat as raw id (maybe slug not found)
			id = token
		}
		if _, ok := seenResolved[id]; !ok {
			seenResolved[id] = struct{}{}
			resolved = append(resolved, id)
		}
	}
	return resolved
}

var (
	practiceQuestionMissing = errors.New("practice question not in published exam")
	practiceOptionMissing   = errors.New("practice option not in question")
)

type practiceCheckRequest struct {
	QuestionID        string   `json:"question_id"`
	SelectedOptionIDs []string `json:"selected_option_ids"`
	SelectedOptionID  string   `json:"selected_option_id"`
}

type PracticeQuestionDTO struct {
	ID         string         `json:"id"`
	DomainID   string         `json:"domain_id"`
	ExamSlug   string         `json:"exam_slug"`
	Prompt     string         `json:"prompt"`
	Scenario   string         `json:"scenario,omitempty"`
	Difficulty string         `json:"difficulty"`
	Position   int            `json:"position"`
	Options    []PublicOption `json:"options"`
}

type PracticeQuestionsDTO struct {
	Questions []PracticeQuestionDTO `json:"questions"`
}

type PracticeProgressAnswer struct {
	QuestionID        string    `json:"question_id"`
	SelectedOptionIDs []string  `json:"selected_option_ids"`
	IsCorrect         bool      `json:"is_correct"`
	AnsweredAt        time.Time `json:"answered_at"`
}

type PracticeProgressDTO struct {
	Answers []PracticeProgressAnswer `json:"answers"`
}

func practiceFeedback(snapshot exams.Snapshot, questionID string, selectedOptionIDs []string) (attempts.CompletedQuestion, error) {
	for _, question := range snapshot.Questions {
		if question.ID != questionID {
			continue
		}
		resolvedIDs := make([]string, 0, len(selectedOptionIDs))
		for _, selectedID := range selectedOptionIDs {
			resolvedID := ""
			for _, option := range question.Options {
				if option.ID == selectedID || option.Key == selectedID {
					resolvedID = option.ID
					break
				}
			}
			if resolvedID == "" {
				return attempts.CompletedQuestion{}, practiceOptionMissing
			}
			resolvedIDs = append(resolvedIDs, resolvedID)
		}
		if len(resolvedIDs) == 0 {
			return attempts.CompletedQuestion{}, practiceOptionMissing
		}
		feedback := attempts.AnswerFeedback{
			QuestionID:        questionID,
			SelectedOptionIDs: resolvedIDs,
			CorrectOptionIDs:  attempts.QuestionCorrectOptionIDs(question),
			IsCorrect:         attempts.SelectionsAreCorrect(question, resolvedIDs),
			Explanation:       question.Explanation,
		}
		return feedbackQuestionResponse(snapshot, feedback), nil
	}
	return attempts.CompletedQuestion{}, practiceQuestionMissing
}
