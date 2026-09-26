package publishing

import (
	"context"
	"errors"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
)

// LinkChecker matches the interface already used by content validation. The
// publishing service accepts either an HTTP- or MCP-supplied checker so both
// transports produce identical preflight results.
type LinkChecker interface {
	InternalExists(context.Context, string) (bool, error)
	ExternalStatus(context.Context, string) (int, error)
}

// PreflightOptions configures validation behavior. CheckExternal mirrors the
// field used by the content validation API so the HTTP admin and MCP layers
// can toggle remote link verification independently.
type PreflightOptions struct {
	CheckExternal bool
	Now           func() func() time.Time
}

// DefaultPreflightOptions returns the standard preflight configuration with
// remote link verification disabled to keep the publishing path deterministic.
func DefaultPreflightOptions() PreflightOptions {
	return PreflightOptions{CheckExternal: false}
}

// NotePreflight validates a note draft against publication rules and returns a
// ValidationOutcome. Identical inputs MUST produce identical outcomes from the
// HTTP admin and MCP callers.
func NotePreflight(ctx context.Context, note content.Note, checker LinkChecker, options PreflightOptions) ValidationOutcome {
	outcome := ValidationOutcome{Errors: []ValidationIssue{}, Warnings: []ValidationIssue{}}
	contentResult := content.ValidateForPublication(ctx, note, contentCheckerAdapter{checker}, content.ValidationOptions{CheckExternal: options.CheckExternal})
	for _, issue := range contentResult.Errors {
		outcome.Errors = append(outcome.Errors, ValidationIssue{Field: issue.Field, Message: issue.Message})
	}
	for _, issue := range contentResult.Warnings {
		outcome.Warnings = append(outcome.Warnings, ValidationIssue{Field: issue.Field, Message: issue.Message})
	}
	return outcome
}

// ExamPreflight validates an exam draft using the canonical exam validation
// rules. The publishing service re-uses exams.ValidateForPublication so the
// shared result matches the existing repository publication path.
func ExamPreflight(_ context.Context, exam exams.Exam) ValidationOutcome {
	outcome := ValidationOutcome{Errors: []ValidationIssue{}, Warnings: []ValidationIssue{}}
	for _, issue := range exams.ValidateForPublication(exam) {
		outcome.Errors = append(outcome.Errors, ValidationIssue{Field: issue.Field, QuestionID: issue.QuestionID, Message: issue.Message})
	}
	return outcome
}

// contentCheckerAdapter wraps a publishing.LinkChecker in the
// content.LinkChecker interface so the canonical validator can be invoked.
type contentCheckerAdapter struct {
	checker LinkChecker
}

func (a contentCheckerAdapter) InternalExists(ctx context.Context, target string) (bool, error) {
	if a.checker == nil {
		return false, errors.New("link checker not configured")
	}
	return a.checker.InternalExists(ctx, target)
}

func (a contentCheckerAdapter) ExternalStatus(ctx context.Context, target string) (int, error) {
	if a.checker == nil {
		return 0, errors.New("link checker not configured")
	}
	return a.checker.ExternalStatus(ctx, target)
}
