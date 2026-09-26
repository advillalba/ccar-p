package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/publishing"
	"github.com/ccar-p/study-platform/internal/store"
)

// stubAdminRepositories is the in-memory test double for the production
// AdminRepositories. Each accessor returns the same backing stub so the
// handler can chain operations (e.g., create then publish) within a single
// request.
type stubAdminRepositories struct {
	notes     *stubAdminContent
	exams     *stubAdminExam
	questions *stubAdminQuestion
	options   *stubAdminOption
	refs      *stubAdminReference
	audit     *stubAdminAudit
	domains   []content.Domain
}

func newStubAdminRepositories() *stubAdminRepositories {
	return &stubAdminRepositories{
		notes:     newStubAdminContent(),
		exams:     newStubAdminExam(),
		questions: newStubAdminQuestion(),
		options:   newStubAdminOption(),
		refs:      newStubAdminReference(),
		audit:     newStubAdminAudit(),
		domains:   []content.Domain{{ID: "domain-cloud", Name: "Cloud Concepts", Slug: "cloud-concepts", Weight: 27, SortOrder: 1}},
	}
}

func (s *stubAdminRepositories) NewContentRepository() AdminContentRepository   { return s.notes }
func (s *stubAdminRepositories) NewExamRepository() AdminExamRepository         { return s.exams }
func (s *stubAdminRepositories) NewQuestionRepository() AdminQuestionRepository { return s.questions }
func (s *stubAdminRepositories) NewOptionRepository() AdminOptionRepository     { return s.options }
func (s *stubAdminRepositories) NewQuestionReferenceRepository() AdminReferenceRepository {
	return s.refs
}
func (s *stubAdminRepositories) NewAuditRepository() AdminAuditRepository { return s.audit }
func (s *stubAdminRepositories) ActiveDomains(_ context.Context) ([]content.Domain, error) {
	out := make([]content.Domain, len(s.domains))
	copy(out, s.domains)
	return out, nil
}
func (s *stubAdminRepositories) UpdateDomain(_ context.Context, id string, weight, sortOrder int, active bool) (content.Domain, error) {
	for i := range s.domains {
		if s.domains[i].ID == id {
			s.domains[i].Weight = weight
			s.domains[i].SortOrder = sortOrder
			s.domains[i].IsActive = active
			return s.domains[i], nil
		}
	}
	return content.Domain{}, errors.New("domain not found")
}

// stubAdminContent is the in-memory AdminContentRepository.
type stubAdminContent struct {
	byID  map[string]content.Note
	all   map[string]content.Note
	order []string
}

func newStubAdminContent() *stubAdminContent {
	return &stubAdminContent{
		byID: map[string]content.Note{},
		all:  map[string]content.Note{},
	}
}

func (s *stubAdminContent) ActiveDomains(_ context.Context) ([]content.Domain, error) {
	return []content.Domain{}, nil
}

func (s *stubAdminContent) NoteByID(_ context.Context, id string) (content.Note, error) {
	if note, ok := s.byID[id]; ok {
		return note, nil
	}
	return content.Note{}, store.ErrPublishedNoteNotFound
}

func (s *stubAdminContent) ListAllNotes(_ context.Context) ([]store.NoteSummary, error) {
	out := make([]store.NoteSummary, 0, len(s.all))
	for _, id := range s.order {
		note := s.all[id]
		out = append(out, store.NoteSummary{
			ID:          note.ID,
			DomainID:    note.DomainID,
			Title:       note.Title,
			Slug:        note.Slug,
			Status:      string(note.Status),
			Version:     note.Version,
			PublishedAt: note.PublishedAt,
			UpdatedAt:   note.UpdatedAt,
		})
	}
	return out, nil
}

func (s *stubAdminContent) CreateNote(_ context.Context, authorID string, input content.NoteInput) (content.Note, error) {
	if err := input.Validate(); err != nil {
		return content.Note{}, err
	}
	note := content.Note{
		ID:         "note-" + input.Slug,
		DomainID:   input.DomainID,
		AuthorID:   authorID,
		Title:      input.Title,
		Slug:       input.Slug,
		Summary:    input.Summary,
		Markdown:   input.Markdown,
		Status:     content.StatusDraft,
		Version:    1,
		Tags:       input.Tags,
		References: input.References,
		UpdatedAt:  time.Now(),
	}
	s.byID[note.ID] = note
	s.all[note.ID] = note
	s.order = append(s.order, note.ID)
	return note, nil
}

func (s *stubAdminContent) UpdateNote(_ context.Context, id, _ string, input content.NoteInput) (content.Note, error) {
	if err := input.Validate(); err != nil {
		return content.Note{}, err
	}
	note, ok := s.byID[id]
	if !ok {
		return content.Note{}, store.ErrPublishedNoteNotFound
	}
	note.Title = input.Title
	note.Slug = input.Slug
	note.Summary = input.Summary
	note.Markdown = input.Markdown
	note.Tags = input.Tags
	note.References = input.References
	note.UpdatedAt = time.Now()
	s.byID[id] = note
	s.all[id] = note
	return note, nil
}

func (s *stubAdminContent) DeleteNote(_ context.Context, id string) error {
	note, ok := s.byID[id]
	if !ok {
		return errors.New("draft note not found")
	}
	if note.Status != content.StatusDraft {
		return errors.New("only draft notes can be deleted")
	}
	delete(s.byID, id)
	delete(s.all, id)
	return nil
}

func (s *stubAdminContent) Publish(_ context.Context, id, _ string, version int) (content.Note, error) {
	note, ok := s.byID[id]
	if !ok {
		return content.Note{}, errors.New("note not found")
	}
	if note.Status != content.StatusDraft {
		return content.Note{}, errors.New("invalid_transition: only drafts can be published")
	}
	now := time.Now()
	note.Status = content.StatusPublished
	note.Version = version
	note.PublishedAt = &now
	note.UpdatedAt = now
	s.byID[id] = note
	s.all[id] = note
	return note, nil
}

func (s *stubAdminContent) Archive(_ context.Context, id, _ string) (content.Note, error) {
	note, ok := s.byID[id]
	if !ok {
		return content.Note{}, errors.New("note not found")
	}
	note.Status = content.StatusArchived
	note.UpdatedAt = time.Now()
	s.byID[id] = note
	s.all[id] = note
	return note, nil
}

// stubAdminExam is the in-memory AdminExamRepository.
type stubAdminExam struct {
	byID map[string]exams.Exam
}

func newStubAdminExam() *stubAdminExam {
	return &stubAdminExam{byID: map[string]exams.Exam{}}
}

func (s *stubAdminExam) ListAllExams(_ context.Context) ([]store.ExamSummary, error) {
	out := make([]store.ExamSummary, 0, len(s.byID))
	for _, exam := range s.byID {
		out = append(out, store.ExamSummary{
			ID:               exam.ID,
			Title:            exam.Title,
			Slug:             exam.Slug,
			Difficulty:       string(exam.Difficulty),
			Status:           string(exam.Status),
			Version:          exam.Version,
			TimeLimitMinutes: exam.TimeLimitMinutes,
			PassPercentage:   exam.PassPercentage,
			QuestionCount:    len(exam.Questions),
			PublishedAt:      exam.PublishedAt,
		})
	}
	return out, nil
}

func (s *stubAdminExam) Create(_ context.Context, exam exams.Exam) (exams.Exam, error) {
	exam.Status = exams.StatusDraft
	exam.Version = 1
	s.byID[exam.ID] = exam
	return exam, nil
}

func (s *stubAdminExam) Update(_ context.Context, exam exams.Exam) error {
	if _, ok := s.byID[exam.ID]; !ok {
		return errors.New("exam not found")
	}
	s.byID[exam.ID] = exam
	return nil
}

func (s *stubAdminExam) Delete(_ context.Context, id string) error {
	delete(s.byID, id)
	return nil
}

func (s *stubAdminExam) Load(_ context.Context, id string) (exams.Exam, error) {
	if exam, ok := s.byID[id]; ok {
		return exam, nil
	}
	return exams.Exam{}, errors.New("exam not found")
}

func (s *stubAdminExam) Publish(_ context.Context, id string) (exams.Snapshot, error) {
	exam, ok := s.byID[id]
	if !ok {
		return exams.Snapshot{}, errors.New("exam not found")
	}
	if verrs := exams.ValidateForPublication(exam); len(verrs) > 0 {
		return exams.Snapshot{}, exams.ValidationErrors(verrs)
	}
	now := time.Now()
	exam.Status = exams.StatusPublished
	exam.Version++
	exam.PublishedAt = &now
	s.byID[id] = exam
	return exams.Snapshot{ExamID: exam.ID, Version: exam.Version, Title: exam.Title, Slug: exam.Slug}, nil
}

func (s *stubAdminExam) Archive(_ context.Context, id string) error {
	exam, ok := s.byID[id]
	if !ok {
		return errors.New("exam not found")
	}
	exam.Status = exams.StatusArchived
	s.byID[id] = exam
	return nil
}

func (s *stubAdminExam) CurrentSnapshot(_ context.Context, id string) (exams.Snapshot, string, error) {
	exam, ok := s.byID[id]
	if !ok {
		return exams.Snapshot{}, "", errors.New("exam not found")
	}
	return exams.Snapshot{ExamID: exam.ID, Version: exam.Version, Title: exam.Title, Slug: exam.Slug}, string(exam.Status), nil
}

// stubAdminQuestion is the in-memory AdminQuestionRepository.
type stubAdminQuestion struct {
	byID  map[string]exams.Question
	order map[string][]string
}

func newStubAdminQuestion() *stubAdminQuestion {
	return &stubAdminQuestion{
		byID:  map[string]exams.Question{},
		order: map[string][]string{},
	}
}

func (s *stubAdminQuestion) Create(_ context.Context, q exams.Question) (exams.Question, error) {
	s.byID[q.ID] = q
	s.order[q.ExamID] = append(s.order[q.ExamID], q.ID)
	return q, nil
}

func (s *stubAdminQuestion) Update(_ context.Context, q exams.Question) error {
	s.byID[q.ID] = q
	return nil
}

func (s *stubAdminQuestion) Delete(_ context.Context, id string) error {
	delete(s.byID, id)
	return nil
}

func (s *stubAdminQuestion) Reorder(_ context.Context, examID string, ids []string) error {
	s.order[examID] = ids
	return nil
}

// stubAdminOption is the in-memory AdminOptionRepository.
type stubAdminOption struct {
	byID map[string]exams.Option
}

func newStubAdminOption() *stubAdminOption { return &stubAdminOption{byID: map[string]exams.Option{}} }

func (s *stubAdminOption) Create(_ context.Context, o exams.Option) (exams.Option, error) {
	s.byID[o.ID] = o
	return o, nil
}
func (s *stubAdminOption) Update(_ context.Context, o exams.Option) error {
	s.byID[o.ID] = o
	return nil
}
func (s *stubAdminOption) Delete(_ context.Context, id string) error               { delete(s.byID, id); return nil }
func (s *stubAdminOption) Reorder(_ context.Context, _ string, ids []string) error { return nil }

// stubAdminReference is the in-memory AdminReferenceRepository.
type stubAdminReference struct {
	byID map[string]exams.Reference
}

func newStubAdminReference() *stubAdminReference {
	return &stubAdminReference{byID: map[string]exams.Reference{}}
}

func (s *stubAdminReference) Create(_ context.Context, r exams.Reference) (exams.Reference, error) {
	s.byID[r.ID] = r
	return r, nil
}
func (s *stubAdminReference) Update(_ context.Context, r exams.Reference) error {
	s.byID[r.ID] = r
	return nil
}
func (s *stubAdminReference) Delete(_ context.Context, id string) error {
	delete(s.byID, id)
	return nil
}
func (s *stubAdminReference) Reorder(_ context.Context, _ string, ids []string) error { return nil }

// stubAdminAudit is the in-memory AdminAuditRepository.
type stubAdminAudit struct {
	events []publishing.AuditEvent
}

func newStubAdminAudit() *stubAdminAudit { return &stubAdminAudit{} }

func (s *stubAdminAudit) RecordPublication(_ context.Context, event publishing.AuditEvent) (publishing.AuditReceipt, error) {
	s.events = append(s.events, event)
	return publishing.AuditReceipt{ID: "receipt-" + event.EntityID, CreatedAt: event.CreatedAt}, nil
}

func (s *stubAdminAudit) ListPublications(_ context.Context, filter publishing.PublicationFilter) ([]publishing.AuditEvent, error) {
	out := make([]publishing.AuditEvent, 0, len(s.events))
	for _, event := range s.events {
		if filter.EntityType != "" && event.EntityType != filter.EntityType {
			continue
		}
		if filter.Action != "" && event.Action != filter.Action {
			continue
		}
		if filter.ActorID != "" && event.ActorID != filter.ActorID {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}

// stubAdminAuthenticator lets tests inject the session for admin endpoints.
type stubAdminAuthenticator struct {
	user    auth.User
	session auth.Session
	err     error
}

func (s *stubAdminAuthenticator) Authenticate(_ context.Context, _ string) (auth.User, auth.Session, error) {
	return s.user, s.session, s.err
}

func adminUser() auth.User {
	return auth.User{ID: "user-admin", Email: "admin@example.com", DisplayName: "Admin", Role: auth.RoleAdmin, IsActive: true}
}

func studentUser() auth.User {
	return auth.User{ID: "user-student", Email: "student@example.com", DisplayName: "Student", Role: auth.RoleStudent, IsActive: true}
}

func newAdminHandler(repos AdminRepositories) *AdminHandler {
	return NewAdminHandler(repos, &stubAdminAuthenticator{user: adminUser(), session: auth.Session{ID: "sess"}}, discardLogger(), time.Now)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newAdminHandlerAs(user auth.User, repos AdminRepositories) *AdminHandler {
	return NewAdminHandler(repos, &stubAdminAuthenticator{user: user, session: auth.Session{ID: "sess"}}, discardLogger(), time.Now)
}

func adminMux(t *testing.T, h *AdminHandler) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func doAdminJSON(mux http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	payload, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func doAdminNoBody(mux http.Handler, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func decodeSuccess(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var env struct {
		Data  map[string]any                  `json:"data"`
		Error *struct{ Code, Message string } `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error != nil {
		t.Fatalf("unexpected error envelope: %v", env.Error)
	}
	return env.Data
}

func decodeError(t *testing.T, body io.Reader) (string, map[string]string) {
	t.Helper()
	var env errorEnvelope
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return env.Error.Code, env.Error.Fields
}

func TestAdminRoutesRequireAuth(t *testing.T) {
	repos := newStubAdminRepositories()
	h := NewAdminHandler(repos, &stubAdminAuthenticator{err: errors.New("no session")}, discardLogger(), time.Now)
	mux := adminMux(t, h)

	w := doAdminNoBody(mux, http.MethodGet, "/api/v1/admin/notes")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAdminRoutesRequireAdminRole(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandlerAs(studentUser(), repos)
	mux := adminMux(t, h)

	w := doAdminNoBody(mux, http.MethodGet, "/api/v1/admin/notes")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminListNotes(t *testing.T) {
	repos := newStubAdminRepositories()
	_, _ = repos.notes.CreateNote(context.Background(), "user-1", content.NoteInput{
		DomainID: "domain-cloud", Title: "Hello", Slug: "hello", Summary: "s", Markdown: "body",
	})
	h := newAdminHandler(repos)
	mux := adminMux(t, h)

	w := doAdminNoBody(mux, http.MethodGet, "/api/v1/admin/notes")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	data := decodeSuccess(t, w.Body)
	notes, _ := data["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
}

func TestAdminCreateNoteValidation(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/notes", map[string]any{"title": ""})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminCreateNoteSucceeds(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/notes", map[string]any{
		"domain_id": "domain-cloud",
		"title":     "Sample Note",
		"slug":      "sample-note",
		"summary":   "Summary",
		"markdown":  "Body",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminPublishNoteTransitionsAndAudits(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.notes.CreateNote(context.Background(), "user-1", content.NoteInput{
		DomainID: "domain-cloud", Title: "Hello", Slug: "hello", Summary: "s", Markdown: "body",
	})

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/notes/note-hello/publish", map[string]any{"reason": "release"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	if len(repos.audit.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(repos.audit.events))
	}
	if repos.audit.events[0].Outcome != "success" {
		t.Fatalf("expected success outcome, got %s", repos.audit.events[0].Outcome)
	}
}

func TestAdminPublishNoteAlreadyPublished(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.notes.CreateNote(context.Background(), "user-1", content.NoteInput{
		DomainID: "domain-cloud", Title: "Hello", Slug: "hello", Summary: "s", Markdown: "body",
	})
	_, _ = repos.notes.Publish(context.Background(), "note-hello", "user-1", 2)

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/notes/note-hello/publish", map[string]any{})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminPreviewNoteDoesNotPersist(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.notes.CreateNote(context.Background(), "user-1", content.NoteInput{
		DomainID: "domain-cloud", Title: "Hello", Slug: "hello", Summary: "s", Markdown: "body",
	})

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/notes/note-hello/preview", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			DryRun  bool   `json:"dry_run"`
			Outcome string `json:"outcome"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.Data.DryRun {
		t.Fatalf("expected dry_run=true")
	}
	if len(repos.audit.events) != 0 {
		t.Fatalf("preview must not write to audit")
	}
}

func TestAdminListPublicationsFilters(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	repos.audit.events = append(repos.audit.events, publishing.AuditEvent{
		EntityType: publishing.EntityNote, EntityID: "note-1", Action: publishing.ActionPublish,
		ActorKind: publishing.ActorUser, ActorID: "admin", CreatedAt: time.Now(),
	})
	repos.audit.events = append(repos.audit.events, publishing.AuditEvent{
		EntityType: publishing.EntityExam, EntityID: "exam-1", Action: publishing.ActionArchive,
		ActorKind: publishing.ActorUser, ActorID: "admin", CreatedAt: time.Now(),
	})

	w := doAdminNoBody(mux, http.MethodGet, "/api/v1/admin/publications?entity=note")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	data := decodeSuccess(t, w.Body)
	items, _ := data["publications"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 filtered event, got %d", len(items))
	}
}

func TestAdminListExams(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.exams.Create(context.Background(), exams.Exam{ID: "exam-1", Title: "Exam", Slug: "exam", Difficulty: "medium"})

	w := doAdminNoBody(mux, http.MethodGet, "/api/v1/admin/exams")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminUpdateExam(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.exams.Create(context.Background(), exams.Exam{ID: "exam-1", Title: "Exam", Slug: "exam", Difficulty: "medium"})

	w := doAdminJSON(mux, http.MethodPut, "/api/v1/admin/exams/exam-1", map[string]any{
		"title": "Updated", "slug": "exam", "difficulty": "medium", "time_limit_minutes": 30, "pass_percentage": 70.0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminDeleteExam(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.exams.Create(context.Background(), exams.Exam{ID: "exam-1", Title: "Exam", Slug: "exam", Difficulty: "medium"})

	w := doAdminNoBody(mux, http.MethodDelete, "/api/v1/admin/exams/exam-1")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminArchiveExamPersistsAudit(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.exams.Create(context.Background(), exams.Exam{
		ID: "exam-1", Title: "Exam", Slug: "exam", Difficulty: "medium",
		Questions: []exams.Question{{ID: "q1", ExamID: "exam-1", Position: 1, Options: []exams.Option{{ID: "o1", QuestionID: "q1", Position: 1}}}},
	})
	_, _ = repos.exams.Publish(context.Background(), "exam-1")

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/exams/exam-1/archive", map[string]any{"reason": "retired"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	if len(repos.audit.events) == 0 {
		t.Fatalf("expected audit event")
	}
	if repos.audit.events[len(repos.audit.events)-1].Action != publishing.ActionArchive {
		t.Fatalf("expected archive action, got %s", repos.audit.events[len(repos.audit.events)-1].Action)
	}
}

func TestAdminQuestionCreate(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/exams/exam-1/questions", map[string]any{
		"prompt": "What is S3?", "position": 1,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminPreviewExamFailure(t *testing.T) {
	repos := newStubAdminRepositories()
	h := newAdminHandler(repos)
	mux := adminMux(t, h)
	_, _ = repos.exams.Create(context.Background(), exams.Exam{ID: "exam-1", Title: "Empty", Slug: "empty", Difficulty: "medium"})

	w := doAdminJSON(mux, http.MethodPost, "/api/v1/admin/exams/exam-1/preview", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			DryRun   bool     `json:"dry_run"`
			Outcome  string   `json:"outcome"`
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.Data.DryRun {
		t.Fatalf("expected dry_run=true")
	}
	if env.Data.Outcome != "failure" {
		t.Fatalf("expected outcome=failure for empty exam, got %s", env.Data.Outcome)
	}
}
