package publishing

import (
	"context"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
)

// EntityType identifies the kind of content being published or archived.
type EntityType string

const (
	EntityNote EntityType = "note"
	EntityExam EntityType = "exam"
)

// Action identifies the publication state transition requested.
type Action string

const (
	ActionPublish Action = "publish"
	ActionArchive Action = "archive"
)

// ActorKind matches the actor_type database enum.
type ActorKind string

const (
	ActorUser         ActorKind = "user"
	ActorServiceToken ActorKind = "service_token"
)

// Actor attributes a publication request to the responsible principal.
// The same struct is produced by the HTTP admin and MCP authentication layers
// so that the publication service can record events identically.
type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
	Name string    `json:"name,omitempty"`
}

// ActorOrZero returns the actor or a zero-value placeholder used in tests.
func (a Actor) OrZero() Actor {
	if a.Kind == "" || a.ID == "" {
		return Actor{Kind: ActorUser, ID: "00000000-0000-0000-0000-000000000000"}
	}
	return a
}

// ValidationIssue is a single field-level problem reported by preflight.
// The message is safe to expose to clients and admins; field is a stable name.
type ValidationIssue struct {
	Field      string `json:"field"`
	Message    string `json:"message"`
	QuestionID string `json:"question_id,omitempty"`
}

// ValidationOutcome aggregates preflight blockers and warnings. Warnings are
// never fatal; errors must be empty for publication to proceed.
type ValidationOutcome struct {
	Errors   []ValidationIssue `json:"errors"`
	Warnings []ValidationIssue `json:"warnings"`
}

// Valid reports whether the preflight passed (no errors).
func (v ValidationOutcome) Valid() bool { return len(v.Errors) == 0 }

// Merge combines two outcomes preserving source order.
func (v ValidationOutcome) Merge(other ValidationOutcome) ValidationOutcome {
	v.Errors = append(v.Errors, other.Errors...)
	v.Warnings = append(v.Warnings, other.Warnings...)
	return v
}

// ExportOutcome mirrors the export_status database enum. Real publications
// persist "not_requested" because no artifact pipeline runs at publication
// time; the remaining values are kept for historical publication_events rows.
type ExportOutcome string

const (
	ExportNotRequested ExportOutcome = "not_requested"
	ExportPending      ExportOutcome = "pending"
	ExportSucceeded    ExportOutcome = "succeeded"
	ExportWarning      ExportOutcome = "warning"
	ExportFailed       ExportOutcome = "failed"
)

// NoteDraft is the minimal input the publishing service requires for a note.
// It is satisfied by the existing content.Note type so callers do not have to
// translate between representations.
type NoteDraft = content.Note

// ExamDraft is the minimal input the publishing service requires for an exam.
// It mirrors the existing exams.Exam type.
type ExamDraft = exams.Exam

// PublicationResult is the structured outcome shared by the HTTP admin and MCP
// transports. Identical fields are returned for both transports so callers can
// render the same UI, audit log, or API response without translation.
type PublicationResult struct {
	EntityType    EntityType        `json:"entity_type"`
	EntityID      string            `json:"entity_id"`
	Action        Action            `json:"action"`
	Outcome       string            `json:"outcome"` // success | warning | failure
	DryRun        bool              `json:"dry_run"`
	Validation    ValidationOutcome `json:"validation"`
	Version       int               `json:"version,omitempty"`
	PublishedAt   *time.Time        `json:"published_at,omitempty"`
	Actor         Actor             `json:"actor"`
	Reason        string            `json:"reason,omitempty"`
	EventID       string            `json:"event_id,omitempty"`
	EventRecorded bool              `json:"event_recorded"`
	CachesFlushed []string          `json:"caches_flushed,omitempty"`
	Notes         []string          `json:"notes,omitempty"`
}

// PublicationInput carries the request parameters shared by publish/archive.
type PublicationInput struct {
	EntityID     string
	Action       Action
	Actor        Actor
	Reason       string
	Metadata     map[string]any
	Now          time.Time
}

// PublicationContext bundles dependencies needed for a publication call.
type PublicationContext struct {
	Context context.Context
	Input   PublicationInput
}

// Reporter receives structured log lines for publication events. The default
// implementation is noop; adapters inject a slog-based reporter.
type Reporter interface {
	PublicationAttempted(ctx context.Context, result PublicationResult)
	PublicationSucceeded(ctx context.Context, result PublicationResult)
	PublicationFailed(ctx context.Context, result PublicationResult, err error)
}

// NoopReporter discards all log calls; useful for tests.
type NoopReporter struct{}

func (NoopReporter) PublicationAttempted(context.Context, PublicationResult) {}
func (NoopReporter) PublicationSucceeded(context.Context, PublicationResult) {}
func (NoopReporter) PublicationFailed(context.Context, PublicationResult, error) {
}
