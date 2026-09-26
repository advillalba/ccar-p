package publishing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
)

// NoteRepository is the publishing-time view of the content repository. The
// production implementation is store.ContentRepository. Repository write
// methods perform row-locked loads, validation, mutations, and version
// increments inside their own transaction; the publishing service composes
// them and adds audit + cache side effects after the repository returns.
type NoteRepository interface {
	LoadForUpdate(ctx context.Context, id string) (content.Note, error)
	LoadBySlug(ctx context.Context, slug string) (content.Note, error)
	Publish(ctx context.Context, id, actorID string, version int) (content.Note, error)
	Archive(ctx context.Context, id, actorID string) (content.Note, error)
}

// ExamRepository is the publishing-time view of the exam repository. The
// production implementation is store.ExamRepository, whose Publish and
// Archive methods already use row locks, validation, and snapshot
// persistence inside a single transaction.
type ExamRepository interface {
	LoadForUpdate(ctx context.Context, id string) (exams.Exam, error)
	LoadBySlug(ctx context.Context, slug string) (exams.Exam, error)
	Publish(ctx context.Context, id, actorID string, version int) (exams.Snapshot, error)
	Archive(ctx context.Context, id, actorID string) (exams.Snapshot, error)
}

// Service orchestrates validation, dry runs, real publication, and archival
// for both notes and exams. The same Service instance is intended to be
// shared between the HTTP admin and MCP transports.
type Service struct {
	Notes       NoteRepository
	Exams       ExamRepository
	Audit       AuditRepository
	Cache       CacheInvalidator
	LinkChecker LinkChecker
	Now         func() time.Time
	Reporter    Reporter
	Options     PreflightOptions
}

// ErrEntityNotFound is returned when the entity id does not exist in its
// repository.
var ErrEntityNotFound = errors.New("publishing: entity not found")

// ErrInvalidTransition is returned when the requested action cannot be applied
// to the entity's current state.
var ErrInvalidTransition = errors.New("publishing: invalid state transition")

// ErrPreflightFailed indicates validation produced blocking errors.
var ErrPreflightFailed = errors.New("publishing: preflight reported errors")

// NewService constructs a publishing Service. Audit is required because every
// real publication emits a publication_event; the other dependencies default
// to no-op implementations so the service can be used in tests without
// wiring every backend.
func NewService(notes NoteRepository, examsRepo ExamRepository, audit AuditRepository) *Service {
	return &Service{
		Notes:       notes,
		Exams:       examsRepo,
		Audit:       audit,
		Cache:       NoopCacheInvalidator{},
		LinkChecker: nil,
		Now:         time.Now,
		Reporter:    NoopReporter{},
		Options:     DefaultPreflightOptions(),
	}
}

// WithCache returns the service configured with the provided cache
// invalidator.
func (s *Service) WithCache(cache CacheInvalidator) *Service {
	s.Cache = cache
	return s
}

// WithLinkChecker returns the service configured with a link checker.
func (s *Service) WithLinkChecker(checker LinkChecker) *Service {
	s.LinkChecker = checker
	return s
}

// WithReporter returns the service configured with a structured reporter.
func (s *Service) WithReporter(reporter Reporter) *Service {
	s.Reporter = reporter
	return s
}

// WithNow returns the service configured with a deterministic clock.
func (s *Service) WithNow(now func() time.Time) *Service {
	if now != nil {
		s.Now = now
	}
	return s
}

// WithOptions returns the service configured with preflight options.
func (s *Service) WithOptions(options PreflightOptions) *Service {
	s.Options = options
	return s
}

// PublishNote runs validation and (when valid) persists a note publication
// with row locks, audit, and cache invalidation.
func (s *Service) PublishNote(ctx context.Context, input PublicationInput) (PublicationResult, error) {
	return s.publishNoteWithMode(ctx, input, false)
}

// DryRunNote validates the note and reports the prospective version and
// export plan without mutating state, audit history, caches, or files.
func (s *Service) DryRunNote(ctx context.Context, input PublicationInput) (PublicationResult, error) {
	return s.publishNoteWithMode(ctx, input, true)
}

func (s *Service) publishNoteWithMode(ctx context.Context, input PublicationInput, dryRun bool) (PublicationResult, error) {
	if input.EntityID == "" {
		return PublicationResult{}, errors.New("publishing: entity id is required")
	}
	if input.Action != ActionPublish && input.Action != ActionArchive {
		return PublicationResult{}, fmt.Errorf("publishing: unsupported action %q", input.Action)
	}
	actor := input.Actor.OrZero()
	now := s.now()

	if dryRun {
		return s.dryRunNote(ctx, input, actor)
	}

	// The repository owns the publication transaction: row-locked load,
	// state validation, mutation, and snapshot/version persistence. We
	// then layer audit + cache side effects on top of that committed work.
	note, err := s.Notes.LoadForUpdate(ctx, input.EntityID)
	if err != nil {
		return s.publishFailure(ctx, PublicationResult{EntityType: EntityNote, EntityID: input.EntityID, Action: input.Action, Actor: actor, Reason: input.Reason}, mapNoteError(err))
	}

	result := PublicationResult{
		EntityType: EntityNote,
		EntityID:   note.ID,
		Action:     input.Action,
		DryRun:     false,
		Actor:      actor,
		Reason:     input.Reason,
	}

	switch input.Action {
	case ActionPublish:
		outcome := NotePreflight(ctx, note, s.LinkChecker, s.Options)
		result.Validation = outcome
		if !outcome.Valid() {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, ErrPreflightFailed)
			return result, ErrPreflightFailed
		}
		proposedVersion := note.Version + 1
		result.Version = proposedVersion
		updated, err := s.Notes.Publish(ctx, note.ID, actor.ID, proposedVersion)
		if err != nil {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, err)
			return result, err
		}
		result.Version = updated.Version
		result.PublishedAt = cloneTime(updated.PublishedAt)
		result.Outcome = "success"
		event := buildAuditEvent(result, updated.Version, updated.PublishedAt, input, now)
		receipt, err := s.Audit.RecordPublication(ctx, event)
		if err != nil {
			result.Notes = appendUnique(result.Notes, "audit recording failed: "+err.Error())
			result.Outcome = "warning"
		} else {
			result.EventID = receipt.ID
			result.EventRecorded = true
		}
		s.applyCacheInvalidation(ctx, EntityNote, updated.ID, updated.Slug, result.Version, &result, result.EventRecorded)
	case ActionArchive:
		if note.Status == content.StatusArchived {
			result.Outcome = "failure"
			result.Notes = appendUnique(result.Notes, "archive: target already archived")
			result.Version = note.Version
			s.reporter().PublicationFailed(ctx, result, ErrInvalidTransition)
			return result, ErrInvalidTransition
		}
		outcome := NotePreflight(ctx, note, s.LinkChecker, s.Options)
		result.Validation = outcome
		result.Version = note.Version
		updated, err := s.Notes.Archive(ctx, note.ID, actor.ID)
		if err != nil {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, err)
			return result, err
		}
		result.Version = updated.Version
		result.Outcome = "success"
		event := buildAuditEvent(result, updated.Version, updated.PublishedAt, input, now)
		receipt, err := s.Audit.RecordPublication(ctx, event)
		if err != nil {
			result.Notes = appendUnique(result.Notes, "audit recording failed: "+err.Error())
			result.Outcome = "warning"
		} else {
			result.EventID = receipt.ID
			result.EventRecorded = true
		}
		s.applyCacheInvalidation(ctx, EntityNote, updated.ID, updated.Slug, result.Version, &result, result.EventRecorded)
	}

	if result.Outcome == "" {
		result.Outcome = "success"
	}
	sort.Strings(result.CachesFlushed)
	s.reporter().PublicationSucceeded(ctx, result)
	return result, nil
}

func (s *Service) dryRunNote(ctx context.Context, input PublicationInput, actor Actor) (PublicationResult, error) {
	if input.EntityID == "" {
		return PublicationResult{}, errors.New("publishing: entity id is required")
	}
	note, err := s.Notes.LoadForUpdate(ctx, input.EntityID)
	if err != nil {
		return PublicationResult{}, mapNoteError(err)
	}
	outcome := NotePreflight(ctx, note, s.LinkChecker, s.Options)
	result := PublicationResult{
		EntityType: EntityNote,
		EntityID:   note.ID,
		Action:     input.Action,
		DryRun:     true,
		Validation: outcome,
		Actor:      actor,
		Reason:     input.Reason,
		Outcome:    "success",
		Notes:      []string{"dry-run: no state, audit, cache, or export changes performed"},
	}
	if input.Action == ActionPublish {
		if !outcome.Valid() {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, ErrPreflightFailed)
			return result, ErrPreflightFailed
		}
		result.Version = note.Version + 1
	} else {
		result.Version = note.Version
	}
	s.reporter().PublicationAttempted(ctx, result)
	return result, nil
}

// PublishExam runs validation and persists an exam publication. The exam
// snapshot is recorded as a new immutable exam_versions row so historical
// attempts remain stable.
func (s *Service) PublishExam(ctx context.Context, input PublicationInput) (PublicationResult, error) {
	return s.publishExamWithMode(ctx, input, false)
}

// DryRunExam validates the exam and reports the prospective version and
// export plan without mutating state, audit history, caches, or files.
func (s *Service) DryRunExam(ctx context.Context, input PublicationInput) (PublicationResult, error) {
	return s.publishExamWithMode(ctx, input, true)
}

func (s *Service) publishExamWithMode(ctx context.Context, input PublicationInput, dryRun bool) (PublicationResult, error) {
	if input.EntityID == "" {
		return PublicationResult{}, errors.New("publishing: entity id is required")
	}
	if input.Action != ActionPublish && input.Action != ActionArchive {
		return PublicationResult{}, fmt.Errorf("publishing: unsupported action %q", input.Action)
	}
	actor := input.Actor.OrZero()
	now := s.now()

	if dryRun {
		return s.dryRunExam(ctx, input, actor)
	}

	exam, err := s.Exams.LoadForUpdate(ctx, input.EntityID)
	if err != nil {
		return s.publishFailure(ctx, PublicationResult{EntityType: EntityExam, EntityID: input.EntityID, Action: input.Action, Actor: actor, Reason: input.Reason}, mapExamError(err))
	}

	result := PublicationResult{
		EntityType: EntityExam,
		EntityID:   exam.ID,
		Action:     input.Action,
		DryRun:     false,
		Actor:      actor,
		Reason:     input.Reason,
	}

	switch input.Action {
	case ActionPublish:
		if exam.Status == exams.StatusArchived {
			result.Outcome = "failure"
			result.Notes = []string{"publish: archived exams cannot be republished"}
			result.Version = exam.Version
			s.reporter().PublicationFailed(ctx, result, ErrInvalidTransition)
			return result, ErrInvalidTransition
		}
		outcome := ExamPreflight(ctx, exam)
		result.Validation = outcome
		if !outcome.Valid() {
			result.Outcome = "failure"
			result.Version = exam.Version
			s.reporter().PublicationFailed(ctx, result, ErrPreflightFailed)
			return result, ErrPreflightFailed
		}
		proposedVersion := exam.Version + 1
		result.Version = proposedVersion
		snapshot, err := s.Exams.Publish(ctx, exam.ID, actor.ID, proposedVersion)
		if err != nil {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, err)
			return result, err
		}
		result.Version = snapshot.Version
		publishedAt := now
		result.PublishedAt = &publishedAt
		result.Outcome = "success"
		event := buildAuditEvent(result, result.Version, result.PublishedAt, input, now)
		receipt, err := s.Audit.RecordPublication(ctx, event)
		if err != nil {
			result.Notes = appendUnique(result.Notes, "audit recording failed: "+err.Error())
			result.Outcome = "warning"
		} else {
			result.EventID = receipt.ID
			result.EventRecorded = true
		}
		s.applyCacheInvalidation(ctx, EntityExam, exam.ID, exam.Slug, result.Version, &result, result.EventRecorded)
	case ActionArchive:
		if exam.Status == exams.StatusArchived {
			result.Outcome = "failure"
			result.Notes = []string{"archive: target already archived"}
			result.Version = exam.Version
			s.reporter().PublicationFailed(ctx, result, ErrInvalidTransition)
			return result, ErrInvalidTransition
		}
		outcome := ExamPreflight(ctx, exam)
		result.Validation = outcome
		result.Version = exam.Version
		snapshot, err := s.Exams.Archive(ctx, exam.ID, actor.ID)
		if err != nil {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, err)
			return result, err
		}
		result.Version = snapshot.Version
		result.Outcome = "success"
		event := buildAuditEvent(result, result.Version, nil, input, now)
		receipt, err := s.Audit.RecordPublication(ctx, event)
		if err != nil {
			result.Notes = appendUnique(result.Notes, "audit recording failed: "+err.Error())
			result.Outcome = "warning"
		} else {
			result.EventID = receipt.ID
			result.EventRecorded = true
		}
		s.applyCacheInvalidation(ctx, EntityExam, exam.ID, exam.Slug, result.Version, &result, result.EventRecorded)
	}

	if result.Outcome == "" {
		result.Outcome = "success"
	}
	sort.Strings(result.CachesFlushed)
	s.reporter().PublicationSucceeded(ctx, result)
	return result, nil
}

func (s *Service) dryRunExam(ctx context.Context, input PublicationInput, actor Actor) (PublicationResult, error) {
	if input.EntityID == "" {
		return PublicationResult{}, errors.New("publishing: entity id is required")
	}
	exam, err := s.Exams.LoadForUpdate(ctx, input.EntityID)
	if err != nil {
		return PublicationResult{}, mapExamError(err)
	}
	outcome := ExamPreflight(ctx, exam)
	result := PublicationResult{
		EntityType: EntityExam,
		EntityID:   exam.ID,
		Action:     input.Action,
		DryRun:     true,
		Validation: outcome,
		Actor:      actor,
		Reason:     input.Reason,
		Outcome:    "success",
		Notes:      []string{"dry-run: no state, audit, cache, or export changes performed"},
	}
	if input.Action == ActionPublish {
		if exam.Status == exams.StatusArchived {
			result.Outcome = "failure"
			result.Notes = []string{"publish: archived exams cannot be republished"}
			s.reporter().PublicationFailed(ctx, result, ErrInvalidTransition)
			return result, ErrInvalidTransition
		}
		if !outcome.Valid() {
			result.Outcome = "failure"
			s.reporter().PublicationFailed(ctx, result, ErrPreflightFailed)
			return result, ErrPreflightFailed
		}
		result.Version = exam.Version + 1
	} else {
		result.Version = exam.Version
	}
	s.reporter().PublicationAttempted(ctx, result)
	return result, nil
}

// applyCacheInvalidation flushes caches for the entity after the publishing
// transaction has committed. Cache invalidation is skipped when the audit
// event was not recorded so the operator can re-trigger it explicitly.
// Failures during flush cannot roll back the published state, so they
// downgrade the result's outcome to a warning.
func (s *Service) applyCacheInvalidation(ctx context.Context, kind EntityType, id, slug string, version int, result *PublicationResult, auditRecorded bool) {
	if s.Cache == nil {
		return
	}
	if !auditRecorded {
		return
	}
	target := InvalidationTarget{EntityType: kind, EntityID: id, Slug: slug, Version: version}
	if err := s.Cache.Invalidate(ctx, target); err != nil {
		result.Notes = appendUnique(result.Notes, "cache invalidation failed: "+err.Error())
		result.Outcome = "warning"
		return
	}
	result.CachesFlushed = append(result.CachesFlushed, cacheLabel(kind, slug))
}

// publishFailure centralises the failure-shaping logic when the service
// cannot even reach validation (e.g., the entity does not exist).
func (s *Service) publishFailure(ctx context.Context, result PublicationResult, err error) (PublicationResult, error) {
	if result.Outcome == "" {
		result.Outcome = "failure"
	}
	s.reporter().PublicationFailed(ctx, result, err)
	return result, err
}

func buildAuditEvent(result PublicationResult, version int, publishedAt *time.Time, input PublicationInput, now time.Time) AuditEvent {
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	return AuditEvent{
		EntityType:   result.EntityType,
		EntityID:     result.EntityID,
		Version:      version,
		Action:       input.Action,
		ActorKind:    result.Actor.Kind,
		ActorID:      result.Actor.ID,
		Reason:       strings.TrimSpace(input.Reason),
		Metadata:     metadata,
		DryRun:       false,
		ExportStatus: ExportNotRequested,
		Outcome:      result.Outcome,
		CreatedAt:    now,
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) reporter() Reporter {
	if s.Reporter != nil {
		return s.Reporter
	}
	return NoopReporter{}
}

func mapNoteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrEntityNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
		return ErrEntityNotFound
	}
	return err
}

func mapExamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrEntityNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
		return ErrEntityNotFound
	}
	return err
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func appendUnique(slice []string, value string) []string {
	if value == "" {
		return slice
	}
	for _, existing := range slice {
		if existing == value {
			return slice
		}
	}
	return append(slice, value)
}

func cacheLabel(kind EntityType, slug string) string {
	return cachePrefix(kind) + ":" + slug
}

func cachePrefix(kind EntityType) string {
	switch kind {
	case EntityNote:
		return "note"
	case EntityExam:
		return "exam"
	default:
		return "entity"
	}
}

// NoopCacheInvalidator discards invalidation calls.
type NoopCacheInvalidator struct{}

// Invalidate returns nil.
func (NoopCacheInvalidator) Invalidate(_ context.Context, _ InvalidationTarget) error { return nil }
