package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/publishing"
	"github.com/ccar-p/study-platform/internal/store"
)

// NotesAPI is the dependency surface required by the note tools. The MCP
// package depends only on the small NoteRepository interface defined in the
// publishing package so wiring is identical to the HTTP admin path.
type NotesAPI interface {
	ListPublishedNotes(ctx context.Context) ([]content.PublishedNoteSummary, error)
	PublishedNoteBySlug(ctx context.Context, slug string) (content.PublishedNote, error)
	CreateNote(ctx context.Context, authorID string, input content.NoteInput) (content.Note, error)
	UpdateNote(ctx context.Context, id, authorID string, input content.NoteInput) (content.Note, error)
}

// NotesService is the subset of the publishing service used by the note
// tools. Either the concrete *publishing.Service or an in-memory stub can
// satisfy this interface, which keeps the package self-contained for tests.
type NotesService interface {
	PublishNote(ctx context.Context, input publishing.PublicationInput) (publishing.PublicationResult, error)
	DryRunNote(ctx context.Context, input publishing.PublicationInput) (publishing.PublicationResult, error)
}

type noteTools struct {
	notes   NotesAPI
	service NotesService
}

// RegisterNotesTools installs the eight note tools in the supplied registry.
func RegisterNotesTools(registry *Registry, notes NotesAPI, service NotesService) error {
	if registry == nil {
		return errors.New("mcp: registry is required")
	}
	if notes == nil {
		return errors.New("mcp: notes repository is required")
	}
	if service == nil {
		return errors.New("mcp: publishing service is required")
	}
	tools := &noteTools{notes: notes, service: service}
	for _, tool := range []*Tool{
		tools.toolListNotes(),
		tools.toolGetNote(),
		tools.toolCreateNote(),
		tools.toolUpdateNote(),
		tools.toolPreviewNote(),
		tools.toolPublishNote(),
		tools.toolArchiveNote(),
	} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func (t *noteTools) toolListNotes() *Tool {
	return &Tool{
		Name:           "list_notes",
		Description:    "List published notes with summary fields only.",
		RequiredScopes: []Scope{ScopeNotesRead},
		InputSchema:    InputSchema{Properties: map[string]*InputSchemaProperty{}},
		Handler: func(ctx *Context, _ json.RawMessage) (any, error) {
			notes, err := t.notes.ListPublishedNotes(ctx.Ctx)
			if err != nil {
				return nil, fmt.Errorf("list_notes: %w", err)
			}
			return map[string]any{"notes": notes}, nil
		},
	}
}

func (t *noteTools) toolGetNote() *Tool {
	return &Tool{
		Name:           "get_note",
		Description:    "Fetch a published note by its slug, including rendered tags and references.",
		RequiredScopes: []Scope{ScopeNotesRead},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"slug": {Type: "string", Description: "Lowercase slug identifier (required)."},
			},
			Required: []string{"slug"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var args struct {
				Slug string `json:"slug"`
			}
			if err := decodeStrict(raw, &args); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object with slug", map[string]string{"arguments": err.Error()})
			}
			if args.Slug == "" {
				return nil, NewToolError("invalid_args", "slug is required", map[string]string{"slug": "is required"})
			}
			note, err := t.notes.PublishedNoteBySlug(ctx.Ctx, args.Slug)
			if err != nil {
				if errors.Is(err, store.ErrPublishedNoteNotFound) {
					return nil, NewToolError("note_not_found", "no published note matches the supplied slug", map[string]string{"slug": "not found"})
				}
				return nil, fmt.Errorf("get_note: %w", err)
			}
			return note, nil
		},
	}
}

func (t *noteTools) toolCreateNote() *Tool {
	return &Tool{
		Name:           "create_note",
		Description:    "Create a new draft note. The note becomes available for publication afterwards.",
		RequiredScopes: []Scope{ScopeNotesWrite},
		InputSchema: InputSchema{
			Properties: noteInputSchema(),
			Required:   []string{"domain_id", "title", "slug", "markdown"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			input, fields, err := decodeNoteInput(raw)
			if err != nil {
				return nil, err
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "note input failed validation", fields)
			}
			if ctx.ContentAuthorID == "" {
				return nil, NewToolError("content_author_unavailable", "no content author is configured for this service token", nil)
			}
			note, err := t.notes.CreateNote(ctx.Ctx, ctx.ContentAuthorID, input)
			if err != nil {
				return nil, mapNoteToolError("create_note", err)
			}
			return note, nil
		},
	}
}

func (t *noteTools) toolUpdateNote() *Tool {
	return &Tool{
		Name:           "update_note",
		Description:    "Update a draft note. The note must remain in draft state.",
		RequiredScopes: []Scope{ScopeNotesWrite},
		InputSchema: InputSchema{
			Properties: func() map[string]*InputSchemaProperty {
				props := noteInputSchema()
				props["id"] = &InputSchemaProperty{Type: "string", Description: "UUID of the draft note (required)."}
				return props
			}(),
			Required: []string{"id", "domain_id", "title", "slug", "markdown"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
			}
			idRaw, ok := payload["id"]
			var wrapper struct {
				ID string `json:"id"`
			}
			if ok {
				if err := json.Unmarshal(idRaw, &wrapper.ID); err != nil {
					return nil, NewToolError("invalid_args", "id must be a string", map[string]string{"id": err.Error()})
				}
			}
			delete(payload, "id")
			noteRaw, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("encode note input: %w", err)
			}
			input, fields, err := decodeNoteInput(noteRaw)
			if err != nil {
				return nil, err
			}
			if wrapper.ID == "" {
				fields["id"] = "is required"
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "note input failed validation", fields)
			}
			if ctx.ContentAuthorID == "" {
				return nil, NewToolError("content_author_unavailable", "no content author is configured for this service token", nil)
			}
			note, err := t.notes.UpdateNote(ctx.Ctx, wrapper.ID, ctx.ContentAuthorID, input)
			if err != nil {
				return nil, mapNoteToolError("update_note", err)
			}
			return note, nil
		},
	}
}

func (t *noteTools) toolPreviewNote() *Tool {
	return &Tool{
		Name:           "preview_note",
		Description:    "Render the supplied markdown into the same HTML used by the public site.",
		RequiredScopes: []Scope{ScopeNotesRead},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"markdown": {Type: "string", Description: "Markdown source to render (required)."},
			},
			Required: []string{"markdown"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var args struct {
				Markdown string `json:"markdown"`
			}
			if err := decodeStrict(raw, &args); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object with markdown", map[string]string{"arguments": err.Error()})
			}
			if args.Markdown == "" {
				return nil, NewToolError("invalid_args", "markdown is required", map[string]string{"markdown": "is required"})
			}
			doc, html, err := content.RenderHTML(args.Markdown)
			if err != nil {
				return nil, NewToolError("invalid_markdown", err.Error(), nil)
			}
			return map[string]any{"document": doc, "html": html}, nil
		},
	}
}

func (t *noteTools) toolPublishNote() *Tool {
	return &Tool{
		Name:           "publish_note",
		Description:    "Run validation and publish a draft note. Audit and cache invalidation are emitted as side effects.",
		RequiredScopes: []Scope{ScopeNotesPublish},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"id":          {Type: "string", Description: "Note UUID (required)."},
				"reason":      {Type: "string", Description: "Operator-supplied reason stored in the audit metadata."},
				"dry_run":     {Type: "boolean", Description: "If true, no audit, cache, or persistence changes occur."},
							},
			Required: []string{"id"},
		},
		Handler: t.dispatch(publishing.ActionPublish, "publish_note"),
	}
}

func (t *noteTools) toolArchiveNote() *Tool {
	return &Tool{
		Name:           "archive_note",
		Description:    "Archive an existing note. Audit and cache invalidation are emitted as side effects.",
		RequiredScopes: []Scope{ScopeNotesPublish},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"id":     {Type: "string", Description: "Note UUID (required)."},
				"reason": {Type: "string", Description: "Operator-supplied reason stored in the audit metadata."},
			},
			Required: []string{"id"},
		},
		Handler: t.dispatch(publishing.ActionArchive, "archive_note"),
	}
}

func (t *noteTools) dispatch(action publishing.Action, tool string) ToolHandler {
	return func(ctx *Context, raw json.RawMessage) (any, error) {
		var args struct {
			ID         string `json:"id"`
			Reason     string `json:"reason"`
			DryRun     bool   `json:"dry_run"`
		}
		if err := decodeStrict(raw, &args); err != nil {
			return nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
		}
		if args.ID == "" {
			return nil, NewToolError("invalid_args", "id is required", map[string]string{"id": "is required"})
		}
		input := publishing.PublicationInput{
			EntityID:     args.ID,
			Action:       action,
			Actor:        ctx.Actor.OrZero(),
			Reason:       args.Reason,
		}
		var (
			result publishing.PublicationResult
			err    error
		)
		if args.DryRun {
			result, err = t.service.DryRunNote(ctx.Ctx, input)
		} else {
			result, err = t.service.PublishNote(ctx.Ctx, input)
		}
		if err != nil {
			return nil, mapPublishingError(tool, err, result.Validation)
		}
		return result, nil
	}
}

func noteInputSchema() map[string]*InputSchemaProperty {
	return map[string]*InputSchemaProperty{
		"domain_id":  {Type: "string", Description: "Domain UUID the note belongs to (required)."},
		"title":      {Type: "string", Description: "Note title (1–200 characters)."},
		"slug":       {Type: "string", Description: "Lowercase slug identifier; must use lowercase letters, digits, and hyphens."},
		"summary":    {Type: "string", Description: "Summary card text (max 500 characters)."},
		"markdown":   {Type: "string", Description: "Markdown body (max 200000 characters)."},
		"tags":       {Type: "array", Items: &InputSchemaProperty{Type: "object", Properties: map[string]*InputSchemaProperty{"name": {Type: "string"}}, Required: []string{"name"}}, Description: "Tag list; each tag is a slug of at most 64 characters."},
		"references": {Type: "array", Items: &InputSchemaProperty{Type: "object", Properties: map[string]*InputSchemaProperty{"title": {Type: "string"}, "url": {Type: "string"}, "citation": {Type: "string"}, "position": {Type: "integer"}}, Required: []string{"title", "position"}}, Description: "References rendered at the bottom of the note."},
	}
}

func decodeNoteInput(raw json.RawMessage) (content.NoteInput, map[string]string, error) {
	var input content.NoteInput
	fields := make(map[string]string)
	if len(raw) == 0 {
		return input, map[string]string{"arguments": "is required"}, nil
	}
	if err := decodeStrict(raw, &input); err != nil {
		return input, nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
	}
	if err := input.Validate(); err != nil {
		var fe content.FieldErrors
		if errors.As(err, &fe) {
			return input, fe, nil
		}
		return input, map[string]string{"arguments": err.Error()}, nil
	}
	return input, fields, nil
}

func mapNoteToolError(tool string, err error) error {
	if err == nil {
		return nil
	}
	var fe content.FieldErrors
	if errors.As(err, &fe) {
		return NewToolError("invalid_args", "note input failed validation", fe)
	}
	if errors.Is(err, store.ErrPublishedNoteNotFound) {
		return NewToolError("note_not_found", "note does not exist or is not in draft state", nil)
	}
	return fmt.Errorf("%s: %w", tool, err)
}

func mapPublishingError(tool string, err error, validation publishing.ValidationOutcome) error {
	switch {
	case errors.Is(err, publishing.ErrPreflightFailed):
		return NewToolError("preflight_failed", "preflight reported blocking errors", validationFieldMap(validation))
	case errors.Is(err, publishing.ErrInvalidTransition):
		return NewToolError("invalid_state", "target state transition is not permitted", nil)
	case errors.Is(err, publishing.ErrEntityNotFound):
		return NewToolError("entity_not_found", "no entity matches the supplied id", nil)
	default:
		return fmt.Errorf("%s: %w", tool, err)
	}
}

func validationFieldMap(outcome publishing.ValidationOutcome) map[string]string {
	fields := make(map[string]string, len(outcome.Errors))
	for _, issue := range outcome.Errors {
		key := issue.Field
		if key == "" {
			key = "validation"
		}
		fields[key] = issue.Message
	}
	return fields
}
