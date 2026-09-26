package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/publishing"
)

const contentAuthorID = "00000000-0000-0000-0000-000000000010"

type writeNotes struct {
	authorID string
}

func (*writeNotes) ListPublishedNotes(context.Context) ([]content.PublishedNoteSummary, error) {
	return nil, nil
}
func (*writeNotes) PublishedNoteBySlug(context.Context, string) (content.PublishedNote, error) {
	return content.PublishedNote{}, nil
}
func (n *writeNotes) CreateNote(_ context.Context, authorID string, input content.NoteInput) (content.Note, error) {
	n.authorID = authorID
	return content.Note{ID: "00000000-0000-0000-0000-000000000011", AuthorID: authorID, Title: input.Title}, nil
}
func (n *writeNotes) UpdateNote(_ context.Context, _ string, authorID string, input content.NoteInput) (content.Note, error) {
	n.authorID = authorID
	return content.Note{ID: "00000000-0000-0000-0000-000000000011", AuthorID: authorID, Title: input.Title}, nil
}

type writeExams struct {
	authorID string
}

func (*writeExams) ListPublishedExams(context.Context) ([]exams.PublishedExamSummary, error) {
	return nil, nil
}
func (*writeExams) PublishedExamBySlug(context.Context, string) (exams.PublishedExam, error) {
	return exams.PublishedExam{}, nil
}
func (*writeExams) Load(context.Context, string) (exams.Exam, error) { return exams.Exam{}, nil }
func (e *writeExams) Create(_ context.Context, exam exams.Exam) (exams.Exam, error) {
	e.authorID = exam.AuthorID
	return exam, nil
}
func (e *writeExams) Update(_ context.Context, exam exams.Exam) error {
	e.authorID = exam.AuthorID
	return nil
}

type writeQuestions struct {
	createError error
}

func (q *writeQuestions) Create(_ context.Context, question exams.Question) (exams.Question, error) {
	if q.createError != nil {
		return exams.Question{}, q.createError
	}
	return question, nil
}
func (*writeQuestions) Update(context.Context, exams.Question) error                 { return nil }
func (*writeQuestions) Patch(context.Context, exams.Question, *[]exams.Option) error { return nil }
func (*writeQuestions) Delete(context.Context, string) error                         { return nil }
func (*writeQuestions) Reorder(context.Context, string, []string) error {
	return nil
}
func (*writeQuestions) Load(context.Context, string) (exams.Question, error) {
	return exams.Question{}, nil
}

type writeOptions struct{}

func (*writeOptions) Create(_ context.Context, option exams.Option) (exams.Option, error) {
	return option, nil
}
func (*writeOptions) Update(context.Context, exams.Option) error { return nil }
func (*writeOptions) Delete(context.Context, string) error       { return nil }
func (*writeOptions) Reorder(context.Context, string, []string) error {
	return nil
}

type writePublishing struct{}

func (*writePublishing) PublishNote(context.Context, publishing.PublicationInput) (publishing.PublicationResult, error) {
	return publishing.PublicationResult{}, nil
}
func (*writePublishing) DryRunNote(context.Context, publishing.PublicationInput) (publishing.PublicationResult, error) {
	return publishing.PublicationResult{}, nil
}
func (*writePublishing) PublishExam(context.Context, publishing.PublicationInput) (publishing.PublicationResult, error) {
	return publishing.PublicationResult{}, nil
}
func (*writePublishing) DryRunExam(context.Context, publishing.PublicationInput) (publishing.PublicationResult, error) {
	return publishing.PublicationResult{}, nil
}

func TestCreateToolsUseConfiguredContentAuthor(t *testing.T) {
	notes := &writeNotes{}
	examRepo := &writeExams{}
	service := &writePublishing{}
	registry := NewRegistry()
	if err := RegisterNotesTools(registry, notes, service); err != nil {
		t.Fatal(err)
	}
	if err := RegisterExamsTools(registry, examRepo, &writeQuestions{}, &writeOptions{}, service); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Ctx: context.Background(), ContentAuthorID: contentAuthorID}

	createNote, _ := registry.Get("create_note")
	if _, err := createNote.Handler(ctx, json.RawMessage(`{"domain_id":"00000000-0000-0000-0000-000000000012","title":"Draft note","slug":"draft-note","markdown":"# Draft"}`)); err != nil {
		t.Fatal(err)
	}
	if notes.authorID != contentAuthorID {
		t.Fatalf("note author = %q, want %q", notes.authorID, contentAuthorID)
	}

	createExam, _ := registry.Get("create_exam")
	if _, err := createExam.Handler(ctx, json.RawMessage(`{"title":"Draft exam","slug":"draft-exam","difficulty":"beginner","time_limit_minutes":20,"pass_percentage":70}`)); err != nil {
		t.Fatal(err)
	}
	if examRepo.authorID != contentAuthorID {
		t.Fatalf("exam author = %q, want %q", examRepo.authorID, contentAuthorID)
	}
}

func TestCreateQuestionSchemaRequiresDomainID(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterExamsTools(registry, &writeExams{}, &writeQuestions{}, &writeOptions{}, &writePublishing{}); err != nil {
		t.Fatal(err)
	}
	createQuestion, _ := registry.Get("create_question")
	required := make(map[string]bool, len(createQuestion.InputSchema.Required))
	for _, field := range createQuestion.InputSchema.Required {
		required[field] = true
	}
	if !required["domain_id"] || !required["difficulty"] {
		t.Fatalf("required = %v", createQuestion.InputSchema.Required)
	}
	if property := createQuestion.InputSchema.Properties["domain_id"]; property == nil || property.Type != "string" {
		t.Fatalf("domain_id schema = %#v", property)
	}
	options := createQuestion.InputSchema.Properties["options"]
	optionExplanationRequired := false
	if options != nil && options.Items != nil {
		for _, field := range options.Items.Required {
			optionExplanationRequired = optionExplanationRequired || field == "explanation"
		}
	}
	if options == nil || options.Items == nil || options.Items.Properties["explanation"] == nil || !optionExplanationRequired {
		t.Fatalf("option schema must require explanation: %#v", options)
	}
	encoded, err := json.Marshal(createQuestion.InputSchema)
	if err != nil || !json.Valid(encoded) || !containsJSONStrings(encoded, `"domain_id"`, `"required"`) {
		t.Fatalf("encoded schema = %s, error = %v", encoded, err)
	}

	_, err = createQuestion.Handler(&Context{Ctx: context.Background()}, json.RawMessage(`{"exam_id":"00000000-0000-0000-0000-000000000011","prompt":"Draft question","difficulty":"beginner"}`))
	toolError, ok := err.(*ToolError)
	if !ok || toolError.Code != "invalid_args" || toolError.Fields["domain_id"] != "is required" {
		t.Fatalf("error = %#v", err)
	}
}

func TestCreateQuestionReturnsRepositoryFieldErrors(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterExamsTools(registry, &writeExams{}, &writeQuestions{createError: exams.FieldErrors{"domain_id": "does not exist"}}, &writeOptions{}, &writePublishing{}); err != nil {
		t.Fatal(err)
	}
	createQuestion, _ := registry.Get("create_question")
	_, err := createQuestion.Handler(&Context{Ctx: context.Background()}, json.RawMessage(`{"exam_id":"00000000-0000-0000-0000-000000000011","domain_id":"00000000-0000-0000-0000-000000000012","prompt":"Draft question","difficulty":"beginner"}`))
	toolError, ok := err.(*ToolError)
	if !ok || toolError.Code != "invalid_args" || toolError.Fields["domain_id"] != "does not exist" {
		t.Fatalf("error = %#v", err)
	}
}

func TestCreateQuestionPersistsOptions(t *testing.T) {
	options := &writeOptions{}
	registry := NewRegistry()
	if err := RegisterExamsTools(registry, &writeExams{}, &writeQuestions{}, options, &writePublishing{}); err != nil {
		t.Fatal(err)
	}
	createQuestion, _ := registry.Get("create_question")
	body := []byte(`{"exam_id":"00000000-0000-0000-0000-000000000011","domain_id":"00000000-0000-0000-0000-000000000012","prompt":"Draft question","difficulty":"beginner","options":[{"key":"A","text":"First","explanation":"First reason","is_correct":true,"position":1},{"key":"B","text":"Second","explanation":"Second reason","is_correct":false,"position":2}]}`)
	if _, err := createQuestion.Handler(&Context{Ctx: context.Background()}, body); err != nil {
		t.Fatal(err)
	}
}

func TestCreateToolsRejectMissingContentAuthor(t *testing.T) {
	notes := &writeNotes{}
	examRepo := &writeExams{}
	service := &writePublishing{}
	registry := NewRegistry()
	if err := RegisterNotesTools(registry, notes, service); err != nil {
		t.Fatal(err)
	}
	if err := RegisterExamsTools(registry, examRepo, &writeQuestions{}, &writeOptions{}, service); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Ctx: context.Background()}

	createNote, _ := registry.Get("create_note")
	if _, err := createNote.Handler(ctx, json.RawMessage(`{"domain_id":"00000000-0000-0000-0000-000000000012","title":"Draft note","slug":"draft-note","markdown":"# Draft"}`)); toolErrorCode(err) != "content_author_unavailable" {
		t.Fatalf("note error = %v", err)
	}

	createExam, _ := registry.Get("create_exam")
	if _, err := createExam.Handler(ctx, json.RawMessage(`{"title":"Draft exam","slug":"draft-exam","difficulty":"beginner","time_limit_minutes":20,"pass_percentage":70}`)); toolErrorCode(err) != "content_author_unavailable" {
		t.Fatalf("exam error = %v", err)
	}
}

func toolErrorCode(err error) string {
	if toolError, ok := err.(*ToolError); ok {
		return toolError.Code
	}
	return ""
}

func containsJSONStrings(document []byte, values ...string) bool {
	for _, value := range values {
		if !bytes.Contains(document, []byte(value)) {
			return false
		}
	}
	return true
}
