package publishing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
)

// stubNoteRepository is a deterministic in-memory implementation of
// NoteRepository used to drive publishing unit tests. Every state change is
// recorded so concurrency tests can assert on the observed order.
type stubNoteRepository struct {
	mu       sync.Mutex
	notes    map[string]content.Note
	locks    map[string]*sync.Mutex
	updates  []noteUpdate
	loadHook func(string)
}

type noteUpdate struct {
	ID       string
	Action   Action
	Version  int
	ActorID  string
	Occurred time.Time
}

func newStubNoteRepository(seed ...content.Note) *stubNoteRepository {
	repo := &stubNoteRepository{notes: make(map[string]content.Note), locks: make(map[string]*sync.Mutex)}
	for _, note := range seed {
		repo.notes[note.ID] = note
	}
	return repo
}

func (r *stubNoteRepository) lockFor(id string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks[id] == nil {
		r.locks[id] = &sync.Mutex{}
	}
	return r.locks[id]
}

func (r *stubNoteRepository) LoadForUpdate(_ context.Context, id string) (content.Note, error) {
	lock := r.lockFor(id)
	if r.loadHook != nil {
		r.loadHook(id)
	}
	lock.Lock()
	defer lock.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	note, ok := r.notes[id]
	if !ok {
		return content.Note{}, ErrEntityNotFound
	}
	return note, nil
}

func (r *stubNoteRepository) LoadBySlug(_ context.Context, slug string) (content.Note, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, note := range r.notes {
		if note.Slug == slug {
			return note, nil
		}
	}
	return content.Note{}, ErrEntityNotFound
}

func (r *stubNoteRepository) Publish(_ context.Context, id, actorID string, version int) (content.Note, error) {
	lock := r.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	note, ok := r.notes[id]
	if !ok {
		return content.Note{}, ErrEntityNotFound
	}
	if note.Status != content.StatusDraft {
		return content.Note{}, fmt.Errorf("cannot publish from %s", note.Status)
	}
	publishedAt := time.Now().UTC()
	note.Status = content.StatusPublished
	note.Version = version
	note.PublishedAt = &publishedAt
	r.notes[id] = note
	r.updates = append(r.updates, noteUpdate{ID: id, Action: ActionPublish, Version: version, ActorID: actorID, Occurred: publishedAt})
	return note, nil
}

func (r *stubNoteRepository) Archive(_ context.Context, id, actorID string) (content.Note, error) {
	lock := r.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	note, ok := r.notes[id]
	if !ok {
		return content.Note{}, ErrEntityNotFound
	}
	if note.Status == content.StatusArchived {
		return content.Note{}, ErrInvalidTransition
	}
	note.Status = content.StatusArchived
	r.notes[id] = note
	r.updates = append(r.updates, noteUpdate{ID: id, Action: ActionArchive, Version: note.Version, ActorID: actorID, Occurred: time.Now().UTC()})
	return note, nil
}

func (r *stubNoteRepository) Updates() []noteUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]noteUpdate, len(r.updates))
	copy(out, r.updates)
	return out
}

// stubExamRepository mirrors stubNoteRepository for exams.
type stubExamRepository struct {
	mu          sync.Mutex
	exams       map[string]exams.Exam
	snapshots   map[string]exams.Snapshot
	updates     []examUpdate
	loadHook    func(string)
	publishHook func(string)
	archiveHook func(string)
}

type examUpdate struct {
	ID       string
	Action   Action
	Version  int
	ActorID  string
	Occurred time.Time
}

func newStubExamRepository(seed ...exams.Exam) *stubExamRepository {
	repo := &stubExamRepository{exams: make(map[string]exams.Exam), snapshots: make(map[string]exams.Snapshot)}
	for _, exam := range seed {
		repo.exams[exam.ID] = exam
	}
	return repo
}

func (r *stubExamRepository) LoadForUpdate(_ context.Context, id string) (exams.Exam, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadHook != nil {
		r.loadHook(id)
	}
	exam, ok := r.exams[id]
	if !ok {
		return exams.Exam{}, ErrEntityNotFound
	}
	return exam, nil
}

func (r *stubExamRepository) LoadBySlug(_ context.Context, slug string) (exams.Exam, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, exam := range r.exams {
		if exam.Slug == slug {
			return exam, nil
		}
	}
	return exams.Exam{}, ErrEntityNotFound
}

func (r *stubExamRepository) Publish(_ context.Context, id, actorID string, version int) (exams.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.publishHook != nil {
		r.publishHook(id)
	}
	exam, ok := r.exams[id]
	if !ok {
		return exams.Snapshot{}, ErrEntityNotFound
	}
	if exam.Status == exams.StatusArchived {
		return exams.Snapshot{}, ErrInvalidTransition
	}
	if errs := exams.ValidateForPublication(exam); len(errs) != 0 {
		return exams.Snapshot{}, exams.ValidationErrors(errs)
	}
	publishedAt := time.Now().UTC()
	exam.Status = exams.StatusPublished
	exam.Version = version
	exam.PublishedAt = &publishedAt
	snapshot, err := exams.BuildSnapshot(exam, version)
	if err != nil {
		return exams.Snapshot{}, err
	}
	r.exams[id] = exam
	r.snapshots[fmt.Sprintf("%s@%d", id, version)] = snapshot
	r.updates = append(r.updates, examUpdate{ID: id, Action: ActionPublish, Version: version, ActorID: actorID, Occurred: publishedAt})
	return snapshot, nil
}

func (r *stubExamRepository) Archive(_ context.Context, id, actorID string) (exams.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archiveHook != nil {
		r.archiveHook(id)
	}
	exam, ok := r.exams[id]
	if !ok {
		return exams.Snapshot{}, ErrEntityNotFound
	}
	if exam.Status == exams.StatusArchived {
		return exams.Snapshot{}, ErrInvalidTransition
	}
	exam.Status = exams.StatusArchived
	r.exams[id] = exam
	r.updates = append(r.updates, examUpdate{ID: id, Action: ActionArchive, Version: exam.Version, ActorID: actorID, Occurred: time.Now().UTC()})
	return exams.Snapshot{ExamID: id, Version: exam.Version}, nil
}

func (r *stubExamRepository) Updates() []examUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]examUpdate, len(r.updates))
	copy(out, r.updates)
	return out
}

// capturingReporter records every event the publishing service emits so tests
// can assert on the lifecycle of a publication.
type capturingReporter struct {
	mu        sync.Mutex
	attempted []PublicationResult
	succeeded []PublicationResult
	failed    []failure
}

type failure struct {
	Result PublicationResult
	Err    error
}

func (r *capturingReporter) PublicationAttempted(_ context.Context, result PublicationResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempted = append(r.attempted, result)
}

func (r *capturingReporter) PublicationSucceeded(_ context.Context, result PublicationResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.succeeded = append(r.succeeded, result)
}

func (r *capturingReporter) PublicationFailed(_ context.Context, result PublicationResult, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, failure{Result: result, Err: err})
}

func (r *capturingReporter) Attempts() []PublicationResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PublicationResult, len(r.attempted))
	copy(out, r.attempted)
	return out
}

func (r *capturingReporter) Successes() []PublicationResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PublicationResult, len(r.succeeded))
	copy(out, r.succeeded)
	return out
}

func (r *capturingReporter) Failures() []failure {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]failure, len(r.failed))
	copy(out, r.failed)
	return out
}

// fixedLinkChecker is a deterministic LinkChecker for tests.
type fixedLinkChecker struct {
	InternalResult bool
	ExternalCode   int
}

func (f fixedLinkChecker) InternalExists(context.Context, string) (bool, error) {
	return f.InternalResult, nil
}
func (f fixedLinkChecker) ExternalStatus(context.Context, string) (int, error) {
	return f.ExternalCode, nil
}

// failingCache simulates a cache backend that returns an error during flush.
type failingCache struct {
	err error
}

func (f failingCache) Invalidate(context.Context, InvalidationTarget) error { return f.err }

// --- Preflight ---------------------------------------------------------

func TestNotePreflightRejectsMissingFieldsAndBrokenLinks(t *testing.T) {
	note := content.Note{Title: " ", Slug: "Invalid", Markdown: "[bad](/missing)", DomainID: ""}
	checker := fixedLinkChecker{InternalResult: false}
	outcome := NotePreflight(context.Background(), note, checker, PreflightOptions{CheckExternal: false})
	if outcome.Valid() {
		t.Fatalf("expected errors, got %#v", outcome)
	}
	if len(outcome.Errors) < 4 {
		t.Fatalf("expected title, slug, domain, link errors; got %#v", outcome)
	}
}

func TestNotePreflightIsIdenticalAcrossCallers(t *testing.T) {
	note := content.Note{Title: "Hello", Slug: "hello", Markdown: "# Hello\n\nText body.", DomainID: "domain-1"}
	checker := fixedLinkChecker{InternalResult: true}
	admin := NotePreflight(context.Background(), note, checker, DefaultPreflightOptions())
	mcp := NotePreflight(context.Background(), note, checker, DefaultPreflightOptions())
	if admin.Valid() != mcp.Valid() || len(admin.Errors) != len(mcp.Errors) {
		t.Fatalf("admin=%#v mcp=%#v", admin, mcp)
	}
}

func TestExamPreflightSurfacesAllBlockers(t *testing.T) {
	exam := exams.Exam{Questions: []exams.Question{{Position: 2, Options: []exams.Option{{Position: 1, Text: "same"}, {Position: 3, Text: " SAME "}}}}}
	outcome := ExamPreflight(context.Background(), exam)
	if outcome.Valid() {
		t.Fatal("expected exam blockers")
	}
}

func TestExamPreflightRequiresEveryOptionExplanation(t *testing.T) {
	_, repo, _, _, _ := newExamService(t)
	exam, err := repo.LoadForUpdate(context.Background(), "exam-1")
	if err != nil {
		t.Fatal(err)
	}
	exam.Questions[0].Options[0].Explanation = ""
	outcome := ExamPreflight(context.Background(), exam)
	for _, blocker := range outcome.Errors {
		if blocker.Field == "options.explanation" {
			return
		}
	}
	t.Fatalf("missing option explanation blocker: %#v", outcome.Errors)
}

// --- Service integration ----------------------------------------------------

func newNoteService(t *testing.T) (*Service, *stubNoteRepository, *RecordingAuditRepository, *RecordingCacheInvalidator, *capturingReporter) {
	t.Helper()
	note := content.Note{ID: "note-1", DomainID: "domain-1", AuthorID: "author-1", Title: "Title", Slug: "title", Markdown: "Body text.", Status: content.StatusDraft, Version: 0}
	repo := newStubNoteRepository(note)
	audit := NewRecordingAuditRepository()
	cache := NewRecordingCacheInvalidator()
	reporter := &capturingReporter{}
	service := NewService(repo, newStubExamRepository(), audit).
		WithCache(cache).
		WithReporter(reporter).
		WithNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })
	return service, repo, audit, cache, reporter
}

func newExamService(t *testing.T) (*Service, *stubExamRepository, *RecordingAuditRepository, *RecordingCacheInvalidator, *capturingReporter) {
	t.Helper()
	question := exams.Question{ID: "q1", DomainID: "domain-1", Prompt: "Pick?", Explanation: "Because.", Difficulty: "beginner", Position: 1, Options: []exams.Option{{ID: "opt-a", Key: "A", Text: "Correct", Explanation: "Correct reason", IsCorrect: true, Position: 1}, {ID: "opt-b", Key: "B", Text: "Wrong", Explanation: "Wrong reason", Position: 2}}}
	exam := exams.Exam{ID: "exam-1", AuthorID: "author-1", Title: "Exam", Slug: "exam", Difficulty: "beginner", TimeLimitMinutes: 10, PassPercentage: 70, Status: exams.StatusDraft, Version: 0, Questions: []exams.Question{question}}
	repo := newStubExamRepository(exam)
	audit := NewRecordingAuditRepository()
	cache := NewRecordingCacheInvalidator()
	reporter := &capturingReporter{}
	service := NewService(newStubNoteRepository(), repo, audit).
		WithCache(cache).
		WithReporter(reporter).
		WithNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })
	return service, repo, audit, cache, reporter
}

func TestNoteDryRunDoesNotMutate(t *testing.T) {
	service, repo, audit, cache, reporter := newNoteService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1", Name: "Admin"}
	result, err := service.DryRunNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.DryRun {
		t.Fatal("expected dry-run flag")
	}
	if result.Version != 1 {
		t.Fatalf("expected prospective version 1, got %d", result.Version)
	}
	if repo.Updates() != nil && len(repo.Updates()) != 0 {
		t.Fatalf("expected no note updates during dry run, got %#v", repo.Updates())
	}
	if len(audit.Events) != 0 {
		t.Fatalf("expected no audit events during dry run, got %#v", audit.Events)
	}
	if len(cache.Targets) != 0 {
		t.Fatalf("expected no cache invalidations during dry run, got %#v", cache.Targets)
	}
	if !strings.Contains(strings.Join(result.Notes, " "), "dry-run") {
		t.Fatalf("expected dry-run note, got %#v", result.Notes)
	}
	if len(reporter.Failures()) != 0 {
		t.Fatalf("expected no failures, got %#v", reporter.Failures())
	}
}

func TestNotePublishRecordsAuditAndInvalidatesCache(t *testing.T) {
	service, repo, audit, cache, reporter := newNoteService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1", Name: "Admin"}
	result, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor, Reason: "approved", Metadata: map[string]any{"ticket": "T-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "success" || result.Version != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !result.EventRecorded || result.EventID == "" {
		t.Fatalf("expected event recorded, got %#v", result)
	}
	if len(audit.Events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audit.Events))
	}
	event := audit.Events[0]
	if event.Action != ActionPublish || event.Version != 1 || event.ActorID != "user-1" || event.DryRun || event.Outcome != "success" {
		t.Fatalf("unexpected audit event: %#v", event)
	}
	if len(cache.Targets) != 1 || cache.Targets[0].Slug != "title" || cache.Targets[0].Version != 1 {
		t.Fatalf("unexpected cache invalidation: %#v", cache.Targets)
	}
	if len(reporter.Successes()) != 1 {
		t.Fatalf("expected one success, got %d", len(reporter.Successes()))
	}
	updates := repo.Updates()
	if len(updates) != 1 || updates[0].Action != ActionPublish || updates[0].Version != 1 {
		t.Fatalf("unexpected repository updates: %#v", updates)
	}
}

func TestNotePublishPreflightFailureSkipsPersistence(t *testing.T) {
	note := content.Note{ID: "note-2", Title: " ", Slug: "BAD", Markdown: "", DomainID: "", Status: content.StatusDraft, Version: 0}
	repo := newStubNoteRepository(note)
	audit := NewRecordingAuditRepository()
	cache := NewRecordingCacheInvalidator()
	reporter := &capturingReporter{}
	service := NewService(repo, newStubExamRepository(), audit).WithCache(cache).WithReporter(reporter)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	result, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-2", Action: ActionPublish, Actor: actor})
	if !errors.Is(err, ErrPreflightFailed) {
		t.Fatalf("expected preflight failure, got %v", err)
	}
	if result.Outcome != "failure" || result.Validation.Valid() {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(repo.Updates()) != 0 {
		t.Fatalf("expected no repository updates, got %#v", repo.Updates())
	}
	if len(audit.Events) != 0 {
		t.Fatalf("expected no audit events, got %#v", audit.Events)
	}
	if len(cache.Targets) != 0 {
		t.Fatalf("expected no cache invalidations, got %#v", cache.Targets)
	}
}

func TestNoteArchiveAfterPublishUsesCurrentVersion(t *testing.T) {
	service, repo, audit, _, _ := newNoteService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	if _, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	result, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionArchive, Actor: actor})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if result.Version != 1 {
		t.Fatalf("expected archived version 1, got %d", result.Version)
	}
	updates := repo.Updates()
	if len(updates) != 2 {
		t.Fatalf("expected two updates, got %#v", updates)
	}
	if updates[1].Action != ActionArchive {
		t.Fatalf("expected second update to be archive, got %#v", updates[1])
	}
	if len(audit.Events) != 2 {
		t.Fatalf("expected two audit events, got %#v", audit.Events)
	}
}

func TestNoteArchiveAlreadyArchivedReturnsInvalidTransition(t *testing.T) {
	service, _, _, _, _ := newNoteService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	if _, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionArchive, Actor: actor}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionArchive, Actor: actor})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestNotePublishUnknownIDReturnsNotFound(t *testing.T) {
	service, _, _, _, _ := newNoteService(t)
	_, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "missing", Action: ActionPublish, Actor: Actor{Kind: ActorUser, ID: "user-1"}})
	if !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("expected ErrEntityNotFound, got %v", err)
	}
}

func TestNotePublishCacheInvalidationFailureProducesWarning(t *testing.T) {
	service, _, audit, _, _ := newNoteService(t)
	failingCache := failingCache{err: errors.New("redis unreachable")}
	service.Cache = failingCache
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	result, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "warning" {
		t.Fatalf("expected warning outcome, got %#v", result)
	}
	if len(audit.Events) != 1 {
		t.Fatalf("expected audit event still recorded, got %#v", audit.Events)
	}
}

func TestNotePublishAuditFailureProducesWarning(t *testing.T) {
	service, _, _, cache, _ := newNoteService(t)
	service.Audit = failingAudit{err: errors.New("database unavailable")}
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	result, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "warning" {
		t.Fatalf("expected warning outcome, got %#v", result)
	}
	if len(cache.Targets) != 0 {
		t.Fatalf("expected no cache invalidation when audit failed, got %#v", cache.Targets)
	}
}

func TestExamPublishRecordsVersionAndSnapshot(t *testing.T) {
	service, repo, audit, cache, _ := newExamService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	result, err := service.PublishExam(context.Background(), PublicationInput{EntityID: "exam-1", Action: ActionPublish, Actor: actor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version != 1 || result.Outcome != "success" {
		t.Fatalf("unexpected result: %#v", result)
	}
	updates := repo.Updates()
	if len(updates) != 1 || updates[0].Version != 1 {
		t.Fatalf("unexpected repository updates: %#v", updates)
	}
	if len(audit.Events) != 1 {
		t.Fatalf("expected one audit event, got %#v", audit.Events)
	}
	if len(cache.Targets) != 1 || cache.Targets[0].Slug != "exam" {
		t.Fatalf("unexpected cache invalidation: %#v", cache.Targets)
	}
}

func TestExamDryRunDoesNotMutate(t *testing.T) {
	service, repo, audit, cache, _ := newExamService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	result, err := service.DryRunExam(context.Background(), PublicationInput{EntityID: "exam-1", Action: ActionPublish, Actor: actor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.DryRun || result.Version != 1 {
		t.Fatalf("unexpected dry run: %#v", result)
	}
	if len(repo.Updates()) != 0 {
		t.Fatalf("expected no repository updates during dry run, got %#v", repo.Updates())
	}
	if len(audit.Events) != 0 || len(cache.Targets) != 0 {
		t.Fatalf("expected no side effects, got audit=%d cache=%d", len(audit.Events), len(cache.Targets))
	}
}

func TestExamPublishPreflightFailureBlocks(t *testing.T) {
	exam := exams.Exam{ID: "exam-bad", AuthorID: "author-1", Title: " ", Difficulty: "beginner", TimeLimitMinutes: 10, PassPercentage: 70, Status: exams.StatusDraft, Version: 0, Questions: []exams.Question{}}
	repo := newStubExamRepository(exam)
	audit := NewRecordingAuditRepository()
	cache := NewRecordingCacheInvalidator()
	reporter := &capturingReporter{}
	service := NewService(newStubNoteRepository(), repo, audit).WithCache(cache).WithReporter(reporter)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	_, err := service.PublishExam(context.Background(), PublicationInput{EntityID: "exam-bad", Action: ActionPublish, Actor: actor})
	if !errors.Is(err, ErrPreflightFailed) {
		t.Fatalf("expected ErrPreflightFailed, got %v", err)
	}
	if len(audit.Events) != 0 || len(cache.Targets) != 0 {
		t.Fatalf("expected no side effects, got audit=%d cache=%d", len(audit.Events), len(cache.Targets))
	}
}

func TestExamArchivedCannotBePublished(t *testing.T) {
	service, _, _, _, _ := newExamService(t)
	actor := Actor{Kind: ActorUser, ID: "user-1"}
	if _, err := service.PublishExam(context.Background(), PublicationInput{EntityID: "exam-1", Action: ActionPublish, Actor: actor}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := service.PublishExam(context.Background(), PublicationInput{EntityID: "exam-1", Action: ActionArchive, Actor: actor}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_, err := service.PublishExam(context.Background(), PublicationInput{EntityID: "exam-1", Action: ActionPublish, Actor: actor})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestServiceActorAwareEventAttributes(t *testing.T) {
	service, _, audit, _, _ := newNoteService(t)
	actor := Actor{Kind: ActorServiceToken, ID: "token-1", Name: "remote-mcp"}
	if _, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: ActionPublish, Actor: actor, Reason: "automated review"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(audit.Events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audit.Events))
	}
	if audit.Events[0].ActorKind != ActorServiceToken || audit.Events[0].ActorID != "token-1" {
		t.Fatalf("expected service-token actor attribution, got %#v", audit.Events[0])
	}
	if audit.Events[0].Reason != "automated review" {
		t.Fatalf("expected reason recorded, got %#v", audit.Events[0])
	}
}

func TestPublishNoteRequiresEntityID(t *testing.T) {
	service, _, _, _, _ := newNoteService(t)
	_, err := service.PublishNote(context.Background(), PublicationInput{Actor: Actor{Kind: ActorUser, ID: "user-1"}})
	if err == nil || !strings.Contains(err.Error(), "entity id") {
		t.Fatalf("expected entity id error, got %v", err)
	}
}

func TestPublishNoteRejectsUnknownAction(t *testing.T) {
	service, _, _, _, _ := newNoteService(t)
	_, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-1", Action: Action("explode"), Actor: Actor{Kind: ActorUser, ID: "user-1"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("expected unsupported action error, got %v", err)
	}
}

func TestConcurrentNotePublishSerializesUpdates(t *testing.T) {
	repo := newStubNoteRepository(content.Note{ID: "note-c", DomainID: "domain-1", AuthorID: "author-1", Title: "Title", Slug: "title", Markdown: "Body", Status: content.StatusDraft, Version: 0})
	audit := NewRecordingAuditRepository()
	cache := NewRecordingCacheInvalidator()
	reporter := &capturingReporter{}
	// Simulate row-lock contention: the first LoadForUpdate blocks until the
	// test signals release; concurrent callers must wait in line.
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	repo.loadHook = func(string) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	}
	service := NewService(repo, newStubExamRepository(), audit).WithCache(cache).WithReporter(reporter)
	actor := Actor{Kind: ActorUser, ID: "user-1"}

	const concurrency = 4
	results := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			_, err := service.PublishNote(context.Background(), PublicationInput{EntityID: "note-c", Action: ActionPublish, Actor: actor})
			results <- err
		}()
	}
	// All callers queue inside LoadForUpdate. None should complete while the
	// first holds the simulated row lock.
	select {
	case err := <-results:
		t.Fatalf("no publication should complete before release, got %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	successes, failures := 0, 0
	for i := 0; i < concurrency; i++ {
		if err := <-results; err != nil {
			failures++
		} else {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful publication under contention, got successes=%d failures=%d", successes, failures)
	}
	if failures != concurrency-1 {
		t.Fatalf("expected %d failed publications under contention, got %d", concurrency-1, failures)
	}
	if updates := repo.Updates(); len(updates) != 1 {
		t.Fatalf("expected only one repository update due to lock serialization, got %#v", updates)
	}
}

// failingAudit simulates a database audit failure.
type failingAudit struct{ err error }

func (f failingAudit) RecordPublication(context.Context, AuditEvent) (AuditReceipt, error) {
	return AuditReceipt{}, f.err
}

func (f failingAudit) ListPublications(context.Context, PublicationFilter) ([]AuditEvent, error) {
	return nil, f.err
}

// --- Audit repo ---------------------------------------------------------

func TestRecordingAuditRepositoryAttributesEvents(t *testing.T) {
	repo := NewRecordingAuditRepository()
	event := AuditEvent{EntityType: EntityNote, EntityID: "n1", Action: ActionPublish, ActorKind: ActorUser, ActorID: "u1"}
	receipt, err := repo.RecordPublication(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.ID == "" {
		t.Fatal("expected receipt id")
	}
	if len(repo.Events) != 1 {
		t.Fatalf("expected one event, got %d", len(repo.Events))
	}
}

func TestRecordingAuditRepositorySnapshotIsDefensive(t *testing.T) {
	repo := NewRecordingAuditRepository()
	_, _ = repo.RecordPublication(context.Background(), AuditEvent{EntityID: "n1"})
	snapshot := repo.Snapshot()
	snapshot[0].EntityID = "mutated"
	if repo.Events[0].EntityID != "n1" {
		t.Fatalf("snapshot mutation leaked into repository: %#v", repo.Events[0])
	}
}

// --- Cache invalidator --------------------------------------------------

func TestMultiCacheInvalidatorDispatchesInOrder(t *testing.T) {
	a := NewRecordingCacheInvalidator()
	b := NewRecordingCacheInvalidator()
	multi := &MultiCacheInvalidator{Backends: []CacheInvalidator{a, b}}
	if err := multi.Invalidate(context.Background(), InvalidationTarget{EntityType: EntityNote, Slug: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Targets) != 1 || len(b.Targets) != 1 {
		t.Fatalf("expected both backends to receive the invalidation: %#v %#v", a.Targets, b.Targets)
	}
}

func TestMultiCacheInvalidatorReportsFirstError(t *testing.T) {
	failing := failingCache{err: errors.New("backend down")}
	recording := NewRecordingCacheInvalidator()
	multi := &MultiCacheInvalidator{Backends: []CacheInvalidator{failing, recording}}
	if err := multi.Invalidate(context.Background(), InvalidationTarget{}); err == nil {
		t.Fatal("expected error from failing backend")
	}
	if len(recording.Targets) != 0 {
		t.Fatalf("second backend should not be reached when first fails")
	}
}

func TestInvalidationTargetLabel(t *testing.T) {
	if got := (InvalidationTarget{EntityType: EntityNote, Slug: "hello"}).Label(); got != "note:hello" {
		t.Fatalf("unexpected note label: %s", got)
	}
	if got := (InvalidationTarget{EntityType: EntityExam, Slug: "exam"}).Label(); got != "exam:exam" {
		t.Fatalf("unexpected exam label: %s", got)
	}
}
