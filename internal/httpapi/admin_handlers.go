package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/publishing"
	"github.com/ccar-p/study-platform/internal/store"
)

// AdminNoteDTO is the admin-facing projection of a note. It is intentionally
// exhaustive: every lifecycle field is included so the admin UI does not need
// to fetch individual notes again after listing.
type AdminNoteDTO struct {
	ID                 string                   `json:"id"`
	DomainID           string                   `json:"domain_id"`
	Title              string                   `json:"title"`
	Slug               string                   `json:"slug"`
	Summary            string                   `json:"summary"`
	Markdown           string                   `json:"markdown"`
	Status             string                   `json:"status"`
	Version            int                      `json:"version"`
	ReadingTimeMinutes int                      `json:"reading_time_minutes"`
	PublishedAt        *time.Time               `json:"published_at,omitempty"`
	UpdatedAt          time.Time                `json:"updated_at"`
	Tags               []content.TagInput       `json:"tags"`
	References         []content.ReferenceInput `json:"references"`
}

// AdminNoteListItem is the lightweight summary used in the listing endpoint.
type AdminNoteListItem struct {
	ID                 string     `json:"id"`
	DomainID           string     `json:"domain_id"`
	Title              string     `json:"title"`
	Slug               string     `json:"slug"`
	Status             string     `json:"status"`
	Version            int        `json:"version"`
	ReadingTimeMinutes int        `json:"reading_time_minutes"`
	PublishedAt        *time.Time `json:"published_at,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// AdminExamDTO mirrors the structure of AdminNoteDTO but for exams.
type AdminExamDTO struct {
	ID               string           `json:"id"`
	Title            string           `json:"title"`
	Slug             string           `json:"slug"`
	Description      string           `json:"description"`
	Difficulty       string           `json:"difficulty"`
	Status           string           `json:"status"`
	Version          int              `json:"version"`
	TimeLimitMinutes int              `json:"time_limit_minutes"`
	PassPercentage   float64          `json:"pass_percentage"`
	PublishedAt      *time.Time       `json:"published_at,omitempty"`
	Questions        []exams.Question `json:"questions"`
}

// AdminExamListItem is the lightweight summary used in the listing endpoint.
type AdminExamListItem struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Slug             string     `json:"slug"`
	Difficulty       string     `json:"difficulty"`
	Status           string     `json:"status"`
	Version          int        `json:"version"`
	TimeLimitMinutes int        `json:"time_limit_minutes"`
	PassPercentage   float64    `json:"pass_percentage"`
	QuestionCount    int        `json:"question_count"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AdminPublicationDTO projects a publishing.AuditEvent for the admin audit
// history view. The fields are aligned with the database schema so the UI can
// render the same data the persistence layer sees.
type AdminPublicationDTO struct {
	ID           string         `json:"id"`
	EntityType   string         `json:"entity_type"`
	EntityID     string         `json:"entity_id"`
	Version      int            `json:"version"`
	Action       string         `json:"action"`
	ActorKind    string         `json:"actor_kind"`
	ActorID      string         `json:"actor_id"`
	Reason       string         `json:"reason"`
	Metadata     map[string]any `json:"metadata"`
	DryRun       bool           `json:"dry_run"`
	ExportStatus string         `json:"export_status"`
	Outcome      string         `json:"outcome"`
	CreatedAt    time.Time      `json:"created_at"`
}

// AdminRepositories aggregates every dependency the admin handler needs. The
// interface is kept narrow so tests can swap the store-backed implementation
// for in-memory fakes without touching the handler code.
type AdminRepositories interface {
	NewContentRepository() AdminContentRepository
	NewExamRepository() AdminExamRepository
	NewQuestionRepository() AdminQuestionRepository
	NewOptionRepository() AdminOptionRepository
	NewQuestionReferenceRepository() AdminReferenceRepository
	NewAuditRepository() AdminAuditRepository
	ActiveDomains(ctx context.Context) ([]content.Domain, error)
	UpdateDomain(ctx context.Context, id string, weight, sortOrder int, active bool) (content.Domain, error)
}

// AdminContentRepository is the slice of the content repository used by the
// admin handler. The same surface is used for note CRUD and for the
// publish/archive/preview workflow.
type AdminContentRepository interface {
	ActiveDomains(ctx context.Context) ([]content.Domain, error)
	NoteByID(ctx context.Context, id string) (content.Note, error)
	ListAllNotes(ctx context.Context) ([]store.NoteSummary, error)
	CreateNote(ctx context.Context, authorID string, input content.NoteInput) (content.Note, error)
	UpdateNote(ctx context.Context, id, authorID string, input content.NoteInput) (content.Note, error)
	DeleteNote(ctx context.Context, id string) error
	Publish(ctx context.Context, id, actorID string, version int) (content.Note, error)
	Archive(ctx context.Context, id, actorID string) (content.Note, error)
}

// AdminExamRepository is the slice of the exam repository used by the admin
// handler. It intentionally mirrors the public repository plus the methods
// that drive publication transitions.
type AdminExamRepository interface {
	ListAllExams(ctx context.Context) ([]store.ExamSummary, error)
	Create(ctx context.Context, exam exams.Exam) (exams.Exam, error)
	Update(ctx context.Context, exam exams.Exam) error
	Delete(ctx context.Context, id string) error
	Load(ctx context.Context, id string) (exams.Exam, error)
	Publish(ctx context.Context, id string) (exams.Snapshot, error)
	Archive(ctx context.Context, id string) error
	CurrentSnapshot(ctx context.Context, id string) (exams.Snapshot, string, error)
}

// AdminQuestionRepository is the slice of the question repository used by the
// admin handler. The same CRUD surface is used by the public handler so the
// admin endpoint does not need its own SQL paths.
type AdminQuestionRepository interface {
	Create(ctx context.Context, question exams.Question) (exams.Question, error)
	Update(ctx context.Context, question exams.Question) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, examID string, questionIDs []string) error
}

// AdminOptionRepository exposes the option CRUD used by the admin handler.
type AdminOptionRepository interface {
	Create(ctx context.Context, option exams.Option) (exams.Option, error)
	Update(ctx context.Context, option exams.Option) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, questionID string, optionIDs []string) error
}

// AdminReferenceRepository exposes the reference CRUD used by the admin
// handler. It is its own interface so the import path does not leak into the
// public schema.
type AdminReferenceRepository interface {
	Create(ctx context.Context, reference exams.Reference) (exams.Reference, error)
	Update(ctx context.Context, reference exams.Reference) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, questionID string, referenceIDs []string) error
}

// AdminAuditRepository exposes the publishing audit surface to the admin
// handler. It is intentionally narrow so tests can stub it with a recorder.
type AdminAuditRepository interface {
	RecordPublication(ctx context.Context, event publishing.AuditEvent) (publishing.AuditReceipt, error)
	ListPublications(ctx context.Context, filter publishing.PublicationFilter) ([]publishing.AuditEvent, error)
}

// StoreRepositories is the production implementation of AdminRepositories. It
// returns live repositories backed by the supplied Store so the admin handler
// can be wired without any dependency injection plumbing.
type StoreRepositories struct {
	Store *store.Store
}

// NewStoreRepositories constructs a StoreRepositories binding the supplied
// store. Each call to the accessor methods returns a fresh repository handle
// because the underlying store is concurrency-safe and the existing repos do
// not carry per-request state.
func NewStoreRepositories(s *store.Store) *StoreRepositories { return &StoreRepositories{Store: s} }

func (r *StoreRepositories) NewContentRepository() AdminContentRepository {
	return store.NewContentRepository(r.Store)
}
func (r *StoreRepositories) NewExamRepository() AdminExamRepository {
	return store.NewExamRepository(r.Store)
}
func (r *StoreRepositories) NewQuestionRepository() AdminQuestionRepository {
	return store.NewQuestionRepository(r.Store)
}
func (r *StoreRepositories) NewOptionRepository() AdminOptionRepository {
	return store.NewOptionRepository(r.Store)
}
func (r *StoreRepositories) NewQuestionReferenceRepository() AdminReferenceRepository {
	return store.NewQuestionReferenceRepository(r.Store)
}
func (r *StoreRepositories) NewAuditRepository() AdminAuditRepository {
	return store.NewStoreAuditRepository(r.Store)
}
func (r *StoreRepositories) ActiveDomains(ctx context.Context) ([]content.Domain, error) {
	return store.NewContentRepository(r.Store).ActiveDomains(ctx)
}
func (r *StoreRepositories) UpdateDomain(ctx context.Context, id string, weight, sortOrder int, active bool) (content.Domain, error) {
	return store.NewContentRepository(r.Store).UpdateDomain(ctx, id, weight, sortOrder, active)
}

// AdminHandler wires every admin endpoint onto the supplied serve mux. Routes
// follow the convention used by the public handlers: registration is opt-in
// and the handler is safe to construct even if main.go never wires it.
type AdminHandler struct {
	repos      AdminRepositories
	auth       Authenticator
	log        *slog.Logger
	now        func() time.Time
	publishing *publishing.Service
}

// NewAdminHandler constructs the handler with all collaborators injected. The
// clock function defaults to time.Now when nil so production callers can omit
// it; tests pass a fixed clock to make preflight results deterministic.
func NewAdminHandler(repos AdminRepositories, auth Authenticator, log *slog.Logger, now func() time.Time) *AdminHandler {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	handler := &AdminHandler{repos: repos, auth: auth, log: log, now: now}
	handler.publishing = publishing.NewService(
		notePublishingRepository{repos.NewContentRepository()},
		examPublishingRepository{repos.NewExamRepository()},
		repos.NewAuditRepository(),
	).WithNow(now)
	return handler
}

func NewAdminHandlerWithPublishing(repos AdminRepositories, auth Authenticator, log *slog.Logger, now func() time.Time, service *publishing.Service) *AdminHandler {
	handler := NewAdminHandler(repos, auth, log, now)
	if service != nil {
		handler.publishing = service
	}
	return handler
}

// NewStorePublishingService builds the publishing.Service backed by the same
// store repositories the admin handler wraps. It exists so main.go can share
// a single service instance between the HTTP admin handlers and the MCP
// transport without duplicating the adapter wiring.
func NewStorePublishingService(repos AdminRepositories) *publishing.Service {
	return publishing.NewService(
		notePublishingRepository{repos.NewContentRepository()},
		examPublishingRepository{repos.NewExamRepository()},
		repos.NewAuditRepository(),
	)
}

type notePublishingRepository struct{ AdminContentRepository }

func (r notePublishingRepository) LoadForUpdate(ctx context.Context, id string) (content.Note, error) {
	return r.NoteByID(ctx, id)
}
func (r notePublishingRepository) LoadBySlug(context.Context, string) (content.Note, error) {
	return content.Note{}, publishing.ErrEntityNotFound
}

type examPublishingRepository struct{ AdminExamRepository }

func (r examPublishingRepository) LoadForUpdate(ctx context.Context, id string) (exams.Exam, error) {
	return r.Load(ctx, id)
}
func (r examPublishingRepository) LoadBySlug(context.Context, string) (exams.Exam, error) {
	return exams.Exam{}, publishing.ErrEntityNotFound
}
func (r examPublishingRepository) Publish(ctx context.Context, id, _ string, _ int) (exams.Snapshot, error) {
	return r.AdminExamRepository.Publish(ctx, id)
}
func (r examPublishingRepository) Archive(ctx context.Context, id, _ string) (exams.Snapshot, error) {
	exam, err := r.Load(ctx, id)
	if err != nil {
		return exams.Snapshot{}, err
	}
	if err := r.AdminExamRepository.Archive(ctx, id); err != nil {
		return exams.Snapshot{}, err
	}
	return exams.Snapshot{ExamID: exam.ID, Version: exam.Version, Title: exam.Title, Slug: exam.Slug}, nil
}

// RegisterRoutes mounts every admin endpoint onto the mux. Each route is
// gated by RequireAuth followed by RequireAdmin so non-administrative users
// receive a 403 instead of 401.
func (h *AdminHandler) RegisterRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return RequireAuth(h.auth, RequireAdmin(next).ServeHTTP)
	}
	mux.HandleFunc("GET /api/v1/admin/notes", gate(h.listNotes))
	mux.HandleFunc("POST /api/v1/admin/notes", gate(h.createNote))
	mux.HandleFunc("GET /api/v1/admin/notes/{id}", gate(h.getNote))
	mux.HandleFunc("PUT /api/v1/admin/notes/{id}", gate(h.updateNote))
	mux.HandleFunc("DELETE /api/v1/admin/notes/{id}", gate(h.deleteNote))
	mux.HandleFunc("POST /api/v1/admin/notes/{id}/publish", gate(h.publishNote))
	mux.HandleFunc("POST /api/v1/admin/notes/{id}/archive", gate(h.archiveNote))
	mux.HandleFunc("POST /api/v1/admin/notes/{id}/preview", gate(h.previewNote))
	mux.HandleFunc("PUT /api/v1/admin/domains/{id}", gate(h.updateDomain))

	mux.HandleFunc("GET /api/v1/admin/exams", gate(h.listExams))
	mux.HandleFunc("POST /api/v1/admin/exams", gate(h.createExam))
	mux.HandleFunc("GET /api/v1/admin/exams/{id}", gate(h.getExam))
	mux.HandleFunc("PUT /api/v1/admin/exams/{id}", gate(h.updateExam))
	mux.HandleFunc("DELETE /api/v1/admin/exams/{id}", gate(h.deleteExam))
	mux.HandleFunc("POST /api/v1/admin/exams/{id}/publish", gate(h.publishExam))
	mux.HandleFunc("POST /api/v1/admin/exams/{id}/archive", gate(h.archiveExam))
	mux.HandleFunc("POST /api/v1/admin/exams/{id}/preview", gate(h.previewExam))
	mux.HandleFunc("POST /api/v1/admin/exams/{id}/questions", gate(h.createQuestion))
	mux.HandleFunc("PUT /api/v1/admin/exams/{id}/questions/{qid}", gate(h.updateQuestion))
	mux.HandleFunc("DELETE /api/v1/admin/exams/{id}/questions/{qid}", gate(h.deleteQuestion))
	mux.HandleFunc("POST /api/v1/admin/exams/{id}/questions/reorder", gate(h.reorderQuestions))
	mux.HandleFunc("POST /api/v1/admin/questions/{qid}/options", gate(h.createOption))
	mux.HandleFunc("PUT /api/v1/admin/questions/{qid}/options/{oid}", gate(h.updateOption))
	mux.HandleFunc("DELETE /api/v1/admin/questions/{qid}/options/{oid}", gate(h.deleteOption))
	mux.HandleFunc("POST /api/v1/admin/questions/{qid}/options/reorder", gate(h.reorderOptions))
	mux.HandleFunc("POST /api/v1/admin/questions/{qid}/references", gate(h.createReference))
	mux.HandleFunc("PUT /api/v1/admin/questions/{qid}/references/{rid}", gate(h.updateReference))
	mux.HandleFunc("DELETE /api/v1/admin/questions/{qid}/references/{rid}", gate(h.deleteReference))

	mux.HandleFunc("GET /api/v1/admin/publications", gate(h.listPublications))
}

// ----- Notes endpoints -----

func (h *AdminHandler) updateDomain(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Weight    int  `json:"weight"`
		SortOrder int  `json:"sort_order"`
		IsActive  bool `json:"is_active"`
	}
	if id == "" || !decodeAdminJSON(w, r, &input) {
		return
	}
	domain, err := h.repos.UpdateDomain(r.Context(), id, input.Weight, input.SortOrder, input.IsActive)
	if err != nil {
		WriteError(w, r, http.StatusUnprocessableEntity, "invalid_domain", err.Error(), nil)
		return
	}
	WriteData(w, r, http.StatusOK, domain)
}

func (h *AdminHandler) listNotes(w http.ResponseWriter, r *http.Request) {
	notes, err := h.repos.NewContentRepository().ListAllNotes(r.Context())
	if err != nil {
		h.log.Error("admin list notes failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	items := make([]AdminNoteListItem, 0, len(notes))
	for _, note := range notes {
		items = append(items, AdminNoteListItem{
			ID:                 note.ID,
			DomainID:           note.DomainID,
			Title:              note.Title,
			Slug:               note.Slug,
			Status:             note.Status,
			Version:            note.Version,
			ReadingTimeMinutes: note.ReadingTimeMinutes,
			PublishedAt:        note.PublishedAt,
			UpdatedAt:          note.UpdatedAt,
		})
	}
	WriteData(w, r, http.StatusOK, map[string]any{"notes": items})
}

func (h *AdminHandler) createNote(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	var input content.NoteInput
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	repo := h.repos.NewContentRepository()
	note, err := repo.CreateNote(r.Context(), user.ID, input)
	if err != nil {
		if fields, ok := err.(content.FieldErrors); ok {
			WriteError(w, r, http.StatusUnprocessableEntity, "validation_failed", "Some fields need attention.", fields)
			return
		}
		h.log.Error("admin create note failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusCreated, adminNoteDTO(note))
}

func (h *AdminHandler) getNote(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "note id is required", nil)
		return
	}
	note, err := h.repos.NewContentRepository().NoteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrPublishedNoteNotFound) {
			WriteError(w, r, http.StatusNotFound, "note_not_found", "note not found", nil)
			return
		}
		h.log.Error("admin get note failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusOK, adminNoteDTO(note))
}

func (h *AdminHandler) updateNote(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "note id is required", nil)
		return
	}
	var input content.NoteInput
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	note, err := h.repos.NewContentRepository().UpdateNote(r.Context(), id, user.ID, input)
	if err != nil {
		if fields, ok := err.(content.FieldErrors); ok {
			WriteError(w, r, http.StatusUnprocessableEntity, "validation_failed", "Some fields need attention.", fields)
			return
		}
		h.log.Error("admin update note failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusOK, adminNoteDTO(note))
}

func (h *AdminHandler) deleteNote(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "note id is required", nil)
		return
	}
	if err := h.repos.NewContentRepository().DeleteNote(r.Context(), id); err != nil {
		h.log.Error("admin delete note failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) publishNote(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "note id is required", nil)
		return
	}
	var body publicationRequest
	if !decodeAdminJSONAllowEmpty(w, r, &body) {
		return
	}
	input := publishing.PublicationInput{EntityID: id, Action: publishing.ActionPublish, Actor: publishing.Actor{Kind: publishing.ActorUser, ID: user.ID, Name: user.Email}, Reason: body.Reason, Metadata: body.Metadata}
	if body.DryRun {
		result, err := h.publishing.DryRunNote(r.Context(), input)
		if err != nil && !errors.Is(err, publishing.ErrPreflightFailed) {
			writePublicationError(w, r, result, err)
			return
		}
		WriteData(w, r, http.StatusOK, result)
		return
	}
	result, err := h.publishing.PublishNote(r.Context(), input)
	if err != nil {
		writePublicationError(w, r, result, err)
		return
	}
	WriteData(w, r, http.StatusOK, result)
}

func (h *AdminHandler) archiveNote(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "note id is required", nil)
		return
	}
	var body publicationRequest
	if !decodeAdminJSONAllowEmpty(w, r, &body) {
		return
	}
	result, err := h.publishing.PublishNote(r.Context(), publishing.PublicationInput{EntityID: id, Action: publishing.ActionArchive, Actor: publishing.Actor{Kind: publishing.ActorUser, ID: user.ID, Name: user.Email}, Reason: body.Reason, Metadata: body.Metadata})
	if err != nil {
		writePublicationError(w, r, result, err)
		return
	}
	WriteData(w, r, http.StatusOK, result)
}

func (h *AdminHandler) previewNote(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "note id is required", nil)
		return
	}
	var body struct {
		Action   string         `json:"action"`
		Reason   string         `json:"reason"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeAdminJSONAllowEmpty(w, r, &body) {
		return
	}
	repo := h.repos.NewContentRepository()
	note, err := repo.NoteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrPublishedNoteNotFound) {
			WriteError(w, r, http.StatusNotFound, "note_not_found", "note not found", nil)
			return
		}
		h.log.Error("admin preview note lookup failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	_, html, renderErr := content.RenderHTML(note.Markdown)
	action := publishing.ActionPublish
	if body.Action == "archive" {
		action = publishing.ActionArchive
	}
	result, _ := h.publishing.DryRunNote(r.Context(), publishing.PublicationInput{EntityID: id, Action: action, Actor: publishing.Actor{Kind: publishing.ActorUser, ID: user.ID, Name: user.Email}, Reason: body.Reason, Metadata: body.Metadata})
	if renderErr != nil {
		result.Validation.Errors = append(result.Validation.Errors, publishing.ValidationIssue{Field: "markdown", Message: renderErr.Error()})
		result.Outcome = "failure"
	}
	WriteData(w, r, http.StatusOK, struct {
		publishing.PublicationResult
		Note AdminNoteDTO `json:"note"`
		HTML string       `json:"html"`
	}{PublicationResult: result, Note: adminNoteDTO(note), HTML: html})
}

// ----- Exams endpoints -----

func (h *AdminHandler) listExams(w http.ResponseWriter, r *http.Request) {
	exams, err := h.repos.NewExamRepository().ListAllExams(r.Context())
	if err != nil {
		h.log.Error("admin list exams failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	items := make([]AdminExamListItem, 0, len(exams))
	for _, exam := range exams {
		items = append(items, AdminExamListItem{
			ID:               exam.ID,
			Title:            exam.Title,
			Slug:             exam.Slug,
			Difficulty:       exam.Difficulty,
			Status:           exam.Status,
			Version:          exam.Version,
			TimeLimitMinutes: exam.TimeLimitMinutes,
			PassPercentage:   exam.PassPercentage,
			QuestionCount:    exam.QuestionCount,
			PublishedAt:      exam.PublishedAt,
			UpdatedAt:        exam.UpdatedAt,
		})
	}
	WriteData(w, r, http.StatusOK, map[string]any{"exams": items})
}

func (h *AdminHandler) createExam(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	var input exams.Exam
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.AuthorID = user.ID
	exam, err := h.repos.NewExamRepository().Create(r.Context(), input)
	if err != nil {
		if fields, ok := examInputFields(err); ok {
			WriteError(w, r, http.StatusUnprocessableEntity, "validation_failed", "Some fields need attention.", fields)
			return
		}
		h.log.Error("admin create exam failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "create_failed", "Exam could not be created.", nil)
		return
	}
	WriteData(w, r, http.StatusCreated, adminExamDTO(exam))
}

func (h *AdminHandler) getExam(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "exam id is required", nil)
		return
	}
	exam, err := h.repos.NewExamRepository().Load(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrPublishedExamNotFound) {
			WriteError(w, r, http.StatusNotFound, "exam_not_found", "exam not found", nil)
			return
		}
		h.log.Error("admin get exam failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusOK, adminExamDTO(exam))
}

func (h *AdminHandler) updateExam(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "exam id is required", nil)
		return
	}
	var input exams.Exam
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.ID = id
	input.AuthorID = user.ID
	if err := h.repos.NewExamRepository().Update(r.Context(), input); err != nil {
		if fields, ok := examInputFields(err); ok {
			WriteError(w, r, http.StatusUnprocessableEntity, "validation_failed", "Some fields need attention.", fields)
			return
		}
		h.log.Error("admin update exam failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "update_failed", err.Error(), nil)
		return
	}
	exam, err := h.repos.NewExamRepository().Load(r.Context(), id)
	if err != nil {
		h.log.Error("admin update exam reload failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusOK, adminExamDTO(exam))
}

func (h *AdminHandler) deleteExam(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "exam id is required", nil)
		return
	}
	if err := h.repos.NewExamRepository().Delete(r.Context(), id); err != nil {
		h.log.Error("admin delete exam failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) publishExam(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "exam id is required", nil)
		return
	}
	var body publicationRequest
	if !decodeAdminJSONAllowEmpty(w, r, &body) {
		return
	}
	input := publishing.PublicationInput{EntityID: id, Action: publishing.ActionPublish, Actor: publishing.Actor{Kind: publishing.ActorUser, ID: user.ID, Name: user.Email}, Reason: body.Reason, Metadata: body.Metadata}
	if body.DryRun {
		result, err := h.publishing.DryRunExam(r.Context(), input)
		if err != nil && !errors.Is(err, publishing.ErrPreflightFailed) {
			writePublicationError(w, r, result, err)
			return
		}
		WriteData(w, r, http.StatusOK, result)
		return
	}
	result, err := h.publishing.PublishExam(r.Context(), input)
	if err != nil {
		writePublicationError(w, r, result, err)
		return
	}
	WriteData(w, r, http.StatusOK, result)
}

func (h *AdminHandler) archiveExam(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "exam id is required", nil)
		return
	}
	var body publicationRequest
	if !decodeAdminJSONAllowEmpty(w, r, &body) {
		return
	}
	result, err := h.publishing.PublishExam(r.Context(), publishing.PublicationInput{EntityID: id, Action: publishing.ActionArchive, Actor: publishing.Actor{Kind: publishing.ActorUser, ID: user.ID, Name: user.Email}, Reason: body.Reason, Metadata: body.Metadata})
	if err != nil {
		writePublicationError(w, r, result, err)
		return
	}
	WriteData(w, r, http.StatusOK, result)
}

func (h *AdminHandler) previewExam(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "exam id is required", nil)
		return
	}
	var body struct {
		Action   string         `json:"action"`
		Reason   string         `json:"reason"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeAdminJSONAllowEmpty(w, r, &body) {
		return
	}
	repo := h.repos.NewExamRepository()
	exam, err := repo.Load(r.Context(), id)
	if err != nil {
		h.log.Error("admin preview exam lookup failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusNotFound, "exam_not_found", "exam not found", nil)
		return
	}
	action := publishing.ActionPublish
	if body.Action == "archive" {
		action = publishing.ActionArchive
	}
	result, _ := h.publishing.DryRunExam(r.Context(), publishing.PublicationInput{EntityID: id, Action: action, Actor: publishing.Actor{Kind: publishing.ActorUser, ID: user.ID, Name: user.Email}, Reason: body.Reason, Metadata: body.Metadata})
	WriteData(w, r, http.StatusOK, struct {
		publishing.PublicationResult
		Exam AdminExamDTO `json:"exam"`
	}{PublicationResult: result, Exam: adminExamDTO(exam)})
}

// ----- Question endpoints -----

func (h *AdminHandler) createQuestion(w http.ResponseWriter, r *http.Request) {
	examID := strings.TrimSpace(r.PathValue("id"))
	var input exams.Question
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.ExamID = examID
	question, err := h.repos.NewQuestionRepository().Create(r.Context(), input)
	if err != nil {
		h.log.Error("admin create question failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "create_failed", err.Error(), nil)
		return
	}
	WriteData(w, r, http.StatusCreated, question)
}

func (h *AdminHandler) updateQuestion(w http.ResponseWriter, r *http.Request) {
	var input exams.Question
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.ID = strings.TrimSpace(r.PathValue("qid"))
	if err := h.repos.NewQuestionRepository().Update(r.Context(), input); err != nil {
		h.log.Error("admin update question failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "update_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) deleteQuestion(w http.ResponseWriter, r *http.Request) {
	qid := strings.TrimSpace(r.PathValue("qid"))
	if qid == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "question id is required", nil)
		return
	}
	if err := h.repos.NewQuestionRepository().Delete(r.Context(), qid); err != nil {
		h.log.Error("admin delete question failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) reorderQuestions(w http.ResponseWriter, r *http.Request) {
	examID := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		QuestionIDs []string `json:"question_ids"`
	}
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	if err := h.repos.NewQuestionRepository().Reorder(r.Context(), examID, body.QuestionIDs); err != nil {
		h.log.Error("admin reorder questions failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "reorder_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Option endpoints -----

func (h *AdminHandler) createOption(w http.ResponseWriter, r *http.Request) {
	var input exams.Option
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.QuestionID = strings.TrimSpace(r.PathValue("qid"))
	option, err := h.repos.NewOptionRepository().Create(r.Context(), input)
	if err != nil {
		h.log.Error("admin create option failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "create_failed", err.Error(), nil)
		return
	}
	WriteData(w, r, http.StatusCreated, option)
}

func (h *AdminHandler) updateOption(w http.ResponseWriter, r *http.Request) {
	var input exams.Option
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.ID = strings.TrimSpace(r.PathValue("oid"))
	if err := h.repos.NewOptionRepository().Update(r.Context(), input); err != nil {
		h.log.Error("admin update option failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "update_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) deleteOption(w http.ResponseWriter, r *http.Request) {
	oid := strings.TrimSpace(r.PathValue("oid"))
	if oid == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "option id is required", nil)
		return
	}
	if err := h.repos.NewOptionRepository().Delete(r.Context(), oid); err != nil {
		h.log.Error("admin delete option failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) reorderOptions(w http.ResponseWriter, r *http.Request) {
	questionID := strings.TrimSpace(r.PathValue("qid"))
	var body struct {
		OptionIDs []string `json:"option_ids"`
	}
	if questionID == "" || !decodeAdminJSON(w, r, &body) {
		return
	}
	if err := h.repos.NewOptionRepository().Reorder(r.Context(), questionID, body.OptionIDs); err != nil {
		WriteError(w, r, http.StatusConflict, "reorder_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Reference endpoints -----

func (h *AdminHandler) createReference(w http.ResponseWriter, r *http.Request) {
	var input exams.Reference
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.QuestionID = strings.TrimSpace(r.PathValue("qid"))
	ref, err := h.repos.NewQuestionReferenceRepository().Create(r.Context(), input)
	if err != nil {
		h.log.Error("admin create reference failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "create_failed", err.Error(), nil)
		return
	}
	WriteData(w, r, http.StatusCreated, ref)
}

func (h *AdminHandler) updateReference(w http.ResponseWriter, r *http.Request) {
	var input exams.Reference
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	input.ID = strings.TrimSpace(r.PathValue("rid"))
	if err := h.repos.NewQuestionReferenceRepository().Update(r.Context(), input); err != nil {
		h.log.Error("admin update reference failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "update_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) deleteReference(w http.ResponseWriter, r *http.Request) {
	rid := strings.TrimSpace(r.PathValue("rid"))
	if rid == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_id", "reference id is required", nil)
		return
	}
	if err := h.repos.NewQuestionReferenceRepository().Delete(r.Context(), rid); err != nil {
		h.log.Error("admin delete reference failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusConflict, "delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Publications audit history -----

func (h *AdminHandler) listPublications(w http.ResponseWriter, r *http.Request) {
	filter := publishing.PublicationFilter{
		EntityType: publishing.EntityType(strings.TrimSpace(r.URL.Query().Get("entity"))),
		Action:     publishing.Action(strings.TrimSpace(r.URL.Query().Get("action"))),
		ActorID:    strings.TrimSpace(r.URL.Query().Get("actor_id")),
	}
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		if ts, err := time.Parse(time.RFC3339, value); err == nil {
			filter.From = ts
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		if ts, err := time.Parse(time.RFC3339, value); err == nil {
			filter.To = ts
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			filter.Limit = n
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("offset")); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			filter.Offset = n
		}
	}
	events, err := h.repos.NewAuditRepository().ListPublications(r.Context(), filter)
	if err != nil {
		h.log.Error("admin list publications failed", "request_id", RequestID(r.Context()), "error", err.Error())
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	out := make([]AdminPublicationDTO, 0, len(events))
	for _, event := range events {
		out = append(out, AdminPublicationDTO{
			ID:           event.ID,
			EntityType:   string(event.EntityType),
			EntityID:     event.EntityID,
			Version:      event.Version,
			Action:       string(event.Action),
			ActorKind:    string(event.ActorKind),
			ActorID:      event.ActorID,
			Reason:       event.Reason,
			Metadata:     event.Metadata,
			DryRun:       event.DryRun,
			ExportStatus: string(event.ExportStatus),
			Outcome:      event.Outcome,
			CreatedAt:    event.CreatedAt,
		})
	}
	WriteData(w, r, http.StatusOK, map[string]any{"publications": out})
}

// ----- Helpers -----

type publicationRequest struct {
	Reason     string         `json:"reason"`
	Metadata   map[string]any `json:"metadata"`
	DryRun     bool           `json:"dry_run"`
}

func examInputFields(err error) (map[string]string, bool) {
	var fields exams.FieldErrors
	if !errors.As(err, &fields) {
		return nil, false
	}
	return map[string]string(fields), true
}

func writePublicationError(w http.ResponseWriter, r *http.Request, result publishing.PublicationResult, err error) {
	switch {
	case errors.Is(err, publishing.ErrEntityNotFound), errors.Is(err, store.ErrPublishedNoteNotFound), errors.Is(err, store.ErrPublishedExamNotFound):
		WriteError(w, r, http.StatusNotFound, "content_not_found", "content not found", nil)
	case errors.Is(err, publishing.ErrPreflightFailed):
		fields := map[string]string{}
		for _, issue := range result.Validation.Errors {
			key := issue.Field
			if issue.QuestionID != "" {
				key = fmt.Sprintf("questions[%s].%s", issue.QuestionID, issue.Field)
			}
			fields[key] = issue.Message
		}
		WriteError(w, r, http.StatusUnprocessableEntity, "validation_failed", "Content cannot be published.", fields)
	case errors.Is(err, publishing.ErrInvalidTransition), errors.Is(err, exams.ErrArchivedCannotPublish):
		WriteError(w, r, http.StatusConflict, "invalid_transition", "The requested publication transition is not allowed.", nil)
	default:
		WriteError(w, r, http.StatusConflict, "publication_failed", "Publication could not be completed.", nil)
	}
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if err := decodeJSONBody(r, destination); err != nil {
		if errors.Is(err, io.EOF) {
			WriteError(w, r, http.StatusBadRequest, "invalid_request", "request body is required", nil)
			return false
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB", nil)
			return false
		}
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid", nil)
		return false
	}
	return true
}

// decodeAdminJSONAllowEmpty is identical to decodeAdminJSON but treats an
// empty body as a valid no-op request. It is used by the preview endpoint
// where the client may not have any reason or metadata to send.
func decodeAdminJSONAllowEmpty(w http.ResponseWriter, r *http.Request, destination any) bool {
	if r.Body == nil {
		return true
	}
	if r.ContentLength == 0 {
		return true
	}
	return decodeAdminJSON(w, r, destination)
}

func adminNoteDTO(note content.Note) AdminNoteDTO {
	return AdminNoteDTO{
		ID:                 note.ID,
		DomainID:           note.DomainID,
		Title:              note.Title,
		Slug:               note.Slug,
		Summary:            note.Summary,
		Markdown:           note.Markdown,
		Status:             string(note.Status),
		Version:            note.Version,
		ReadingTimeMinutes: note.ReadingTimeMinutes,
		PublishedAt:        note.PublishedAt,
		UpdatedAt:          note.UpdatedAt,
		Tags:               note.Tags,
		References:         note.References,
	}
}

func adminExamDTO(exam exams.Exam) AdminExamDTO {
	return AdminExamDTO{
		ID:               exam.ID,
		Title:            exam.Title,
		Slug:             exam.Slug,
		Description:      exam.Description,
		Difficulty:       string(exam.Difficulty),
		Status:           string(exam.Status),
		Version:          exam.Version,
		TimeLimitMinutes: exam.TimeLimitMinutes,
		PassPercentage:   exam.PassPercentage,
		PublishedAt:      exam.PublishedAt,
		Questions:        exam.Questions,
	}
}

func validationFieldMap(issues []content.ValidationIssue) map[string]string {
	out := make(map[string]string, len(issues))
	for _, issue := range issues {
		out[issue.Field] = issue.Message
	}
	return out
}

func examValidationFields(verrs exams.ValidationErrors) map[string]string {
	out := make(map[string]string, len(verrs))
	for _, err := range verrs {
		field := err.Field
		if err.QuestionID != "" {
			field = fmt.Sprintf("questions[%s].%s", err.QuestionID, err.Field)
		}
		out[field] = err.Message
	}
	return out
}

func publishValidationFromResult(result content.ValidationResult) publishing.ValidationOutcome {
	outcome := publishing.ValidationOutcome{}
	for _, err := range result.Errors {
		outcome.Errors = append(outcome.Errors, publishing.ValidationIssue{Field: err.Field, Message: err.Message})
	}
	for _, warn := range result.Warnings {
		outcome.Warnings = append(outcome.Warnings, publishing.ValidationIssue{Field: warn.Field, Message: warn.Message})
	}
	return outcome
}

func publishingValidationFromExamErrors(errs exams.ValidationErrors) publishing.ValidationOutcome {
	outcome := publishing.ValidationOutcome{}
	for _, err := range errs {
		field := err.Field
		if err.QuestionID != "" {
			field = fmt.Sprintf("questions[%s].%s", err.QuestionID, err.Field)
		}
		outcome.Errors = append(outcome.Errors, publishing.ValidationIssue{Field: field, Message: err.Message})
	}
	return outcome
}

func previewNotes(action publishing.Action, validation content.ValidationResult) []string {
	notes := []string{}
	if !validation.Valid() {
		notes = append(notes, fmt.Sprintf("preflight reported %d errors", len(validation.Errors)))
	}
	if action == publishing.ActionArchive {
		notes = append(notes, "archive is irreversible and removes the entry from the public catalogue")
	}
	return notes
}

// Ensure the publishing package's audit event is recognised so it can carry
// the database identifier through to the admin audit history projection.
var _ = publishing.AuditEvent{}

// Avoid an unused import when the file is compiled without tests.
var _ = json.RawMessage{}
var _ = auth.RoleAdmin
