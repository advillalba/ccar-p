package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ccar-p/study-platform/internal/exams"
	"github.com/ccar-p/study-platform/internal/publishing"
	"github.com/ccar-p/study-platform/internal/store"
)

// ExamsAPI exposes the read/write surface that the exam tools rely on. The
// methods are subsets of store.ExamRepository / store.QuestionRepository so
// the production implementation can be wired directly.
type ExamsAPI interface {
	ListPublishedExams(ctx context.Context) ([]exams.PublishedExamSummary, error)
	PublishedExamBySlug(ctx context.Context, slug string) (exams.PublishedExam, error)
	Load(ctx context.Context, id string) (exams.Exam, error)
	Create(ctx context.Context, exam exams.Exam) (exams.Exam, error)
	Update(ctx context.Context, exam exams.Exam) error
}

// QuestionsAPI exposes the question mutation surface.
type QuestionsAPI interface {
	Create(ctx context.Context, question exams.Question) (exams.Question, error)
	Update(ctx context.Context, question exams.Question) error
	Patch(ctx context.Context, question exams.Question, options *[]exams.Option) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, examID string, questionIDs []string) error
	Load(ctx context.Context, id string) (exams.Question, error)
}

// OptionsAPI exposes the option mutation surface required by create/update question.
type OptionsAPI interface {
	Create(ctx context.Context, option exams.Option) (exams.Option, error)
	Update(ctx context.Context, option exams.Option) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, questionID string, optionIDs []string) error
}

// ExamsService mirrors NotesService but for exams.
type ExamsService interface {
	PublishExam(ctx context.Context, input publishing.PublicationInput) (publishing.PublicationResult, error)
	DryRunExam(ctx context.Context, input publishing.PublicationInput) (publishing.PublicationResult, error)
}

type examTools struct {
	exams     ExamsAPI
	questions QuestionsAPI
	options   OptionsAPI
	service   ExamsService
}

// RegisterExamsTools installs the twelve exam tools.
func RegisterExamsTools(registry *Registry, exams ExamsAPI, questions QuestionsAPI, options OptionsAPI, service ExamsService) error {
	if registry == nil {
		return errors.New("mcp: registry is required")
	}
	if exams == nil {
		return errors.New("mcp: exams repository is required")
	}
	if questions == nil {
		return errors.New("mcp: questions repository is required")
	}
	if service == nil {
		return errors.New("mcp: publishing service is required")
	}
	tools := &examTools{exams: exams, questions: questions, options: options, service: service}
	for _, tool := range []*Tool{
		tools.toolListExams(),
		tools.toolGetExam(),
		tools.toolCreateExam(),
		tools.toolUpdateExam(),
		tools.toolPublishExam(),
		tools.toolArchiveExam(),
		tools.toolCreateQuestion(),
		tools.toolUpdateQuestion(),
		tools.toolPatchQuestion(),
		tools.toolDeleteQuestion(),
		tools.toolReorderQuestions(),
		tools.toolValidateExam(),
	} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func (t *examTools) toolListExams() *Tool {
	return &Tool{
		Name:           "list_exams",
		Description:    "List published exams with summary fields only.",
		RequiredScopes: []Scope{ScopeExamsRead},
		InputSchema:    InputSchema{Properties: map[string]*InputSchemaProperty{}},
		Handler: func(ctx *Context, _ json.RawMessage) (any, error) {
			items, err := t.exams.ListPublishedExams(ctx.Ctx)
			if err != nil {
				return nil, fmt.Errorf("list_exams: %w", err)
			}
			return map[string]any{"exams": items}, nil
		},
	}
}

func (t *examTools) toolGetExam() *Tool {
	return &Tool{
		Name:           "get_exam",
		Description:    "Fetch a published exam by its slug. Includes question explanations and option explanations/is_correct for authoring review.",
		RequiredScopes: []Scope{ScopeExamsRead},
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
			exam, err := t.exams.PublishedExamBySlug(ctx.Ctx, args.Slug)
			if err != nil {
				if errors.Is(err, store.ErrPublishedExamNotFound) {
					return nil, NewToolError("exam_not_found", "no published exam matches the supplied slug", map[string]string{"slug": "not found"})
				}
				return nil, fmt.Errorf("get_exam: %w", err)
			}
			return exam, nil
		},
	}
}

func (t *examTools) toolCreateExam() *Tool {
	return &Tool{
		Name:           "create_exam",
		Description:    "Create a new draft exam. The exam remains in draft state until validated and published.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		InputSchema: InputSchema{
			Properties: examInputSchema(false),
			Required:   []string{"title", "slug", "difficulty"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			exam, fields, err := decodeExamInput(raw, false)
			if err != nil {
				return nil, err
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "exam input failed validation", fields)
			}
			if ctx.ContentAuthorID == "" {
				return nil, NewToolError("content_author_unavailable", "no content author is configured for this service token", nil)
			}
			exam.AuthorID = ctx.ContentAuthorID
			created, err := t.exams.Create(ctx.Ctx, exam)
			if err != nil {
				return nil, NewToolError("create_exam_failed", err.Error(), nil)
			}
			return created, nil
		},
	}
}

func (t *examTools) toolUpdateExam() *Tool {
	return &Tool{
		Name:           "update_exam",
		Description:    "Update a draft exam. The exam must remain in draft state.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		InputSchema: InputSchema{
			Properties: examInputSchema(true),
			Required:   []string{"id", "title", "slug", "difficulty"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			exam, fields, err := decodeExamInput(raw, true)
			if err != nil {
				return nil, err
			}
			if exam.ID == "" {
				fields["id"] = "is required"
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "exam input failed validation", fields)
			}
			if ctx.ContentAuthorID == "" {
				return nil, NewToolError("content_author_unavailable", "no content author is configured for this service token", nil)
			}
			exam.AuthorID = ctx.ContentAuthorID
			if err := t.exams.Update(ctx.Ctx, exam); err != nil {
				return nil, NewToolError("update_exam_failed", err.Error(), nil)
			}
			return map[string]any{"id": exam.ID, "status": "draft"}, nil
		},
	}
}

func (t *examTools) toolPublishExam() *Tool {
	return &Tool{
		Name:           "publish_exam",
		Description:    "Run validation and publish a draft exam. The publishing service handles snapshot persistence, audit, and cache invalidation.",
		RequiredScopes: []Scope{ScopeExamsPublish},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"id":          {Type: "string", Description: "Exam UUID (required)."},
				"reason":      {Type: "string", Description: "Operator-supplied reason stored in the audit metadata."},
				"dry_run":     {Type: "boolean", Description: "If true, no audit, cache, or persistence changes occur."},
							},
			Required: []string{"id"},
		},
		Handler: t.dispatch(publishing.ActionPublish, "publish_exam"),
	}
}

func (t *examTools) toolArchiveExam() *Tool {
	return &Tool{
		Name:           "archive_exam",
		Description:    "Archive an existing exam. Audit and cache invalidation are emitted as side effects.",
		RequiredScopes: []Scope{ScopeExamsPublish},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"id":     {Type: "string", Description: "Exam UUID (required)."},
				"reason": {Type: "string", Description: "Operator-supplied reason stored in the audit metadata."},
			},
			Required: []string{"id"},
		},
		Handler: t.dispatch(publishing.ActionArchive, "archive_exam"),
	}
}

func (t *examTools) toolCreateQuestion() *Tool {
	return &Tool{
		Name:           "create_question",
		Description:    "Create a question attached to a draft exam. Position is auto-assigned.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		InputSchema: InputSchema{
			Properties: questionInputSchema(false),
			Required:   []string{"exam_id", "domain_id", "prompt", "difficulty"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			question, fields, err := decodeQuestionInput(raw, false)
			if err != nil {
				return nil, err
			}
			if question.ExamID == "" {
				fields["exam_id"] = "is required"
			}
			if question.DomainID == "" {
				fields["domain_id"] = "is required"
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "question input failed validation", fields)
			}
			created, err := t.questions.Create(ctx.Ctx, question)
			if err != nil {
				return nil, mapQuestionToolError("create_question", err)
			}
			if t.options != nil && len(question.Options) > 0 {
				created.Options, err = t.createOptions(ctx, created.ID, question.Options, fields)
				if err != nil {
					return nil, err
				}
			}
			return created, nil
		},
	}
}

func (t *examTools) toolUpdateQuestion() *Tool {
	return &Tool{
		Name:           "update_question",
		Description:    "Update an existing question on a draft exam.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		InputSchema: InputSchema{
			Properties: questionInputSchema(true),
			Required:   []string{"id", "exam_id", "domain_id", "prompt", "difficulty"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			question, fields, err := decodeQuestionInput(raw, true)
			if err != nil {
				return nil, err
			}
			if question.ID == "" {
				fields["id"] = "is required"
			}
			if question.ExamID == "" {
				fields["exam_id"] = "is required"
			}
			if question.DomainID == "" {
				fields["domain_id"] = "is required"
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "question input failed validation", fields)
			}
			if err := t.questions.Update(ctx.Ctx, question); err != nil {
				return nil, mapQuestionToolError("update_question", err)
			}
			if t.options != nil && len(question.Options) > 0 {
				updated, err := t.questions.Load(ctx.Ctx, question.ID)
				if err != nil {
					return nil, mapQuestionToolError("update_question", err)
				}
				if err := t.replaceOptions(ctx, question.ID, updated.Options); err != nil {
					return nil, err
				}
				updated.Options, err = t.createOptions(ctx, question.ID, question.Options, fields)
				if err != nil {
					return nil, err
				}
				return map[string]any{"id": updated.ID, "status": "updated", "options": updated.Options}, nil
			}
			return map[string]any{"id": question.ID, "status": "updated"}, nil
		},
	}
}

type patchOption struct {
	Key         string  `json:"key"`
	Text        *string `json:"text"`
	Explanation *string `json:"explanation"`
	IsCorrect   *bool   `json:"is_correct"`
	Position    *int    `json:"position"`
}

func (t *examTools) toolPatchQuestion() *Tool {
	return &Tool{
		Name:           "patch_question",
		Description:    "Patch an existing question (draft or published) to fix prompt, scenario, explanation (also accepted as 'explanations') or option explanations. Only supplied fields are updated; ideal for fixing explanation errors without resending the whole question. option_explanations patches option explanations by key without resending texts. For published exams the live snapshot is updated immediately.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		Mutation:       true,
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"id":           {Type: "string", Description: "Question UUID to patch (required)."},
				"domain_id":    {Type: "string", Description: "Active domain UUID (optional, keeps current if omitted)."},
				"prompt":       {Type: "string", Description: "Question prompt (optional)."},
				"scenario":     {Type: "string", Description: "Optional scenario shown above the prompt (optional)."},
				"explanation":  {Type: "string", Description: "Explanation revealed after grading (optional). Alias 'explanations' also accepted."},
				"explanations": {Type: "string", Description: "Alias for 'explanation' (optional)."},
				"difficulty":   {Type: "string", Enum: []string{"beginner", "intermediate", "advanced", "exam_scenarios"}, Description: "Difficulty classification (optional)."},
				"options": {
					Type: "array",
					Items: &InputSchemaProperty{
						Type: "object",
						Properties: map[string]*InputSchemaProperty{
							"key":         {Type: "string", Description: "Single-letter answer key A-Z (required)."},
							"text":        {Type: "string", Description: "Option text (optional on patch; keeps current if omitted)."},
							"explanation": {Type: "string", Description: "Explanation for this option (optional)."},
							"is_correct":  {Type: "boolean", Description: "True for correct options (optional on patch; keeps current if omitted)."},
							"position":    {Type: "integer", Description: "1-based render order (ignored; re-derived)."},
						},
						Required: []string{"key"},
					},
					Description: "If supplied, merges with current options by key (only key is required on patch). Omitted text/is_correct keep current values. Replaces all options atomically; at least two with at least one correct after merge. Use this to fix option explanations without resending full texts.",
				},
				"option_explanations": {
					Type: "array",
					Items: &InputSchemaProperty{
						Type: "object",
						Properties: map[string]*InputSchemaProperty{
							"key":         {Type: "string", Description: "Option key A-Z to patch."},
							"explanation": {Type: "string", Description: "New explanation for this option (empty clears)."},
						},
						Required: []string{"key"},
					},
					Description: "Alternative to options: patch only option explanations by key. Each entry updates that option's explanation; other fields unchanged.",
				},
			},
			Required: []string{"id"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var payload struct {
				ID                 string         `json:"id"`
				DomainID           *string        `json:"domain_id"`
				Prompt             *string        `json:"prompt"`
				Scenario           *string        `json:"scenario"`
				Explanation        *string        `json:"explanation"`
				Explanations       *string        `json:"explanations"`
				Difficulty         *string        `json:"difficulty"`
				Options            *[]patchOption `json:"options"`
				OptionExplanations *[]struct {
					Key         string  `json:"key"`
					Explanation *string `json:"explanation"`
				} `json:"option_explanations"`
			}
			if err := decodeStrict(raw, &payload); err != nil {
				return nil, err
			}
			if payload.ID == "" {
				return nil, NewToolError("invalid_args", "id is required", map[string]string{"id": "is required"})
			}
			existing, err := t.questions.Load(ctx.Ctx, payload.ID)
			if err != nil {
				return nil, NewToolError("not_found", "question not found", map[string]string{"id": "not found"})
			}
			// Merge fields
			patched := existing
			if payload.DomainID != nil {
				if *payload.DomainID == "" {
					return nil, NewToolError("invalid_args", "domain_id cannot be empty", map[string]string{"domain_id": "cannot be empty"})
				}
				patched.DomainID = *payload.DomainID
			}
			if payload.Prompt != nil {
				if *payload.Prompt == "" {
					return nil, NewToolError("invalid_args", "prompt cannot be empty", map[string]string{"prompt": "cannot be empty"})
				}
				patched.Prompt = *payload.Prompt
			}
			if payload.Scenario != nil {
				patched.Scenario = *payload.Scenario
			}
			if payload.Explanations != nil && payload.Explanation == nil {
				payload.Explanation = payload.Explanations
			}
			if payload.Explanation != nil {
				patched.Explanation = *payload.Explanation
			}
			if payload.Difficulty != nil {
				if *payload.Difficulty == "" {
					return nil, NewToolError("invalid_args", "difficulty cannot be empty", map[string]string{"difficulty": "cannot be empty"})
				}
				patched.Difficulty = exams.Difficulty(*payload.Difficulty)
			}
			if err := patched.ValidateInput(); err != nil {
				return nil, mapQuestionToolError("patch_question", err)
			}
			var mergedOptions *[]exams.Option
			if payload.Options != nil && payload.OptionExplanations != nil {
				return nil, NewToolError("invalid_args", "provide either options or option_explanations, not both", map[string]string{"options": "conflicts with option_explanations"})
			}
			if payload.Options != nil {
				if len(*payload.Options) == 0 {
					return nil, NewToolError("invalid_args", "options cannot be empty", map[string]string{"options": "cannot be empty"})
				}
				byKey := make(map[string]exams.Option, len(existing.Options))
				for _, o := range existing.Options {
					byKey[o.Key] = o
				}
				merged := make([]exams.Option, 0, len(*payload.Options))
				seen := make(map[string]struct{}, len(*payload.Options))
				for idx, po := range *payload.Options {
					if po.Key == "" {
						return nil, NewToolError("invalid_args", "option key is required", map[string]string{fmt.Sprintf("options[%d].key", idx): "is required"})
					}
					if _, dup := seen[po.Key]; dup {
						return nil, NewToolError("invalid_args", "duplicate option key", map[string]string{fmt.Sprintf("options[%d].key", idx): "duplicate"})
					}
					seen[po.Key] = struct{}{}
					base, ok := byKey[po.Key]
					if !ok {
						if po.Text == nil || strings.TrimSpace(*po.Text) == "" {
							return nil, NewToolError("invalid_args", "new option requires text", map[string]string{fmt.Sprintf("options[%d].text", idx): "is required for new key"})
						}
						if po.IsCorrect == nil {
							return nil, NewToolError("invalid_args", "new option requires is_correct", map[string]string{fmt.Sprintf("options[%d].is_correct", idx): "is required for new key"})
						}
						base = exams.Option{Key: po.Key, QuestionID: patched.ID, Position: idx + 1}
					}
					if po.Text != nil {
						base.Text = *po.Text
					}
					if po.Explanation != nil {
						base.Explanation = *po.Explanation
					}
					if po.IsCorrect != nil {
						base.IsCorrect = *po.IsCorrect
					}
					base.Key = po.Key
					base.Position = idx + 1
					base.QuestionID = patched.ID
					if err := base.ValidateInput(); err != nil {
						return nil, mapQuestionToolError("patch_question", err)
					}
					merged = append(merged, base)
				}
				mergedOptions = &merged
			} else if payload.OptionExplanations != nil {
				if len(*payload.OptionExplanations) == 0 {
					return nil, NewToolError("invalid_args", "option_explanations cannot be empty", map[string]string{"option_explanations": "cannot be empty"})
				}
				byKey := make(map[string]int, len(existing.Options))
				for i, o := range existing.Options {
					byKey[o.Key] = i
				}
				merged := make([]exams.Option, len(existing.Options))
				copy(merged, existing.Options)
				for idx, pe := range *payload.OptionExplanations {
					if pe.Key == "" {
						return nil, NewToolError("invalid_args", "option key is required", map[string]string{fmt.Sprintf("option_explanations[%d].key", idx): "is required"})
					}
					pos, ok := byKey[pe.Key]
					if !ok {
						return nil, NewToolError("invalid_args", "option key not found", map[string]string{fmt.Sprintf("option_explanations[%d].key", idx): "not found"})
					}
					if pe.Explanation != nil {
						merged[pos].Explanation = *pe.Explanation
					} else {
						merged[pos].Explanation = ""
					}
					if err := merged[pos].ValidateInput(); err != nil {
						return nil, mapQuestionToolError("patch_question", err)
					}
				}
				mergedOptions = &merged
			}
			if err := t.questions.Patch(ctx.Ctx, patched, mergedOptions); err != nil {
				return nil, mapQuestionToolError("patch_question", err)
			}
			if mergedOptions != nil && len(*mergedOptions) > 0 {
				updated, err := t.questions.Load(ctx.Ctx, patched.ID)
				if err == nil {
					return map[string]any{"id": patched.ID, "status": "patched", "explanation": patched.Explanation, "options": updated.Options}, nil
				}
			}
			return map[string]any{"id": patched.ID, "status": "patched", "explanation": patched.Explanation}, nil
		},
	}
}

func (t *examTools) createOptions(ctx *Context, questionID string, inputs []exams.Option, fields map[string]string) ([]exams.Option, error) {
	created := make([]exams.Option, 0, len(inputs))
	for index, option := range inputs {
		option.QuestionID = questionID
		option.Position = index + 1
		option.ID = ""
		saved, err := t.options.Create(ctx.Ctx, option)
		if err != nil {
			return nil, mapQuestionToolError("create_question", err)
		}
		created = append(created, saved)
	}
	return created, nil
}

func (t *examTools) replaceOptions(ctx *Context, questionID string, existing []exams.Option) error {
	for _, option := range existing {
		if err := t.options.Delete(ctx.Ctx, option.ID); err != nil {
			return mapQuestionToolError("update_question", err)
		}
	}
	return nil
}

func (t *examTools) toolDeleteQuestion() *Tool {
	return &Tool{
		Name:           "delete_question",
		Description:    "Delete a question from a draft exam.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"id": {Type: "string", Description: "Question UUID (required)."},
			},
			Required: []string{"id"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID string `json:"id"`
			}
			if err := decodeStrict(raw, &args); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object with id", map[string]string{"arguments": err.Error()})
			}
			if args.ID == "" {
				return nil, NewToolError("invalid_args", "id is required", map[string]string{"id": "is required"})
			}
			if err := t.questions.Delete(ctx.Ctx, args.ID); err != nil {
				return nil, NewToolError("delete_question_failed", err.Error(), nil)
			}
			return map[string]any{"id": args.ID, "deleted": true}, nil
		},
	}
}

func (t *examTools) toolReorderQuestions() *Tool {
	return &Tool{
		Name:           "reorder_questions",
		Description:    "Reorder questions within a draft exam using the supplied UUID list.",
		RequiredScopes: []Scope{ScopeExamsWrite},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"exam_id":      {Type: "string", Description: "Draft exam UUID (required)."},
				"question_ids": {Type: "array", Items: &InputSchemaProperty{Type: "string"}, Description: "Question UUIDs in the new order (required, must contain every question exactly once)."},
			},
			Required: []string{"exam_id", "question_ids"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var args struct {
				ExamID      string   `json:"exam_id"`
				QuestionIDs []string `json:"question_ids"`
			}
			if err := decodeStrict(raw, &args); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
			}
			if args.ExamID == "" {
				return nil, NewToolError("invalid_args", "exam_id is required", map[string]string{"exam_id": "is required"})
			}
			if len(args.QuestionIDs) == 0 {
				return nil, NewToolError("invalid_args", "question_ids is required", map[string]string{"question_ids": "is required"})
			}
			if err := t.questions.Reorder(ctx.Ctx, args.ExamID, args.QuestionIDs); err != nil {
				return nil, NewToolError("reorder_failed", err.Error(), nil)
			}
			return map[string]any{"exam_id": args.ExamID, "question_ids": args.QuestionIDs}, nil
		},
	}
}

func (t *examTools) toolValidateExam() *Tool {
	return &Tool{
		Name:           "validate_exam",
		Description:    "Run the publication validator against the supplied exam payload without persisting any changes.",
		RequiredScopes: []Scope{ScopeExamsRead},
		InputSchema: InputSchema{
			Properties: examInputSchema(true),
			Required:   []string{"title", "difficulty"},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			exam, fields, err := decodeExamInput(raw, false)
			if err != nil {
				return nil, err
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "exam payload failed validation", fields)
			}
			issues := exams.ValidateForPublication(exam)
			return map[string]any{
				"valid":  len(issues) == 0,
				"errors": issues,
			}, nil
		},
	}
}

func (t *examTools) dispatch(action publishing.Action, tool string) ToolHandler {
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
			result, err = t.service.DryRunExam(ctx.Ctx, input)
		} else {
			result, err = t.service.PublishExam(ctx.Ctx, input)
		}
		if err != nil {
			return nil, mapPublishingError(tool, err, result.Validation)
		}
		return result, nil
	}
}

func examInputSchema(requireID bool) map[string]*InputSchemaProperty {
	props := map[string]*InputSchemaProperty{
		"title":              {Type: "string", Description: "Exam title (required)."},
		"slug":               {Type: "string", Description: "Lowercase slug identifier."},
		"description":        {Type: "string", Description: "Short description rendered on the public page."},
		"difficulty":         {Type: "string", Enum: []string{"beginner", "intermediate", "advanced", "exam_scenarios"}, Description: "Difficulty classification."},
		"time_limit_minutes": {Type: "integer", Description: "Time limit for attempts in minutes."},
		"pass_percentage":    {Type: "number", Description: "Required pass percentage (0–100)."},
	}
	if requireID {
		props["id"] = &InputSchemaProperty{Type: "string", Description: "Draft exam UUID (required)."}
	}
	return props
}

func decodeExamInput(raw json.RawMessage, requireID bool) (exams.Exam, map[string]string, error) {
	fields := make(map[string]string)
	var payload struct {
		ID               string           `json:"id"`
		AuthorID         string           `json:"author_id"`
		Title            string           `json:"title"`
		Slug             string           `json:"slug"`
		Description      string           `json:"description"`
		Difficulty       exams.Difficulty `json:"difficulty"`
		TimeLimitMinutes int              `json:"time_limit_minutes"`
		PassPercentage   float64          `json:"pass_percentage"`
	}
	if len(raw) == 0 {
		return exams.Exam{}, map[string]string{"arguments": "is required"}, nil
	}
	if err := decodeStrict(raw, &payload); err != nil {
		return exams.Exam{}, nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
	}
	exam := exams.Exam{
		ID:               payload.ID,
		AuthorID:         payload.AuthorID,
		Title:            payload.Title,
		Slug:             payload.Slug,
		Description:      payload.Description,
		Difficulty:       payload.Difficulty,
		TimeLimitMinutes: payload.TimeLimitMinutes,
		PassPercentage:   payload.PassPercentage,
	}
	if requireID && exam.ID == "" {
		fields["id"] = "is required"
	}
	if exam.Title == "" {
		fields["title"] = "is required"
	}
	if exam.Slug == "" {
		fields["slug"] = "is required"
	}
	if exam.Difficulty == "" {
		fields["difficulty"] = "is required"
	}
	if exam.TimeLimitMinutes < 0 {
		fields["time_limit_minutes"] = "must be non-negative"
	}
	if exam.PassPercentage < 0 || exam.PassPercentage > 100 {
		fields["pass_percentage"] = "must be between 0 and 100"
	}
	return exam, fields, nil
}

func questionInputSchema(requireID bool) map[string]*InputSchemaProperty {
	props := map[string]*InputSchemaProperty{
		"exam_id":     {Type: "string", Description: "Draft exam UUID the question belongs to (required)."},
		"domain_id":   {Type: "string", Description: "Active domain UUID used for analytics (required)."},
		"prompt":      {Type: "string", Description: "Question prompt (required)."},
		"scenario":    {Type: "string", Description: "Optional scenario shown above the prompt."},
		"explanation": {Type: "string", Description: "Explanation revealed after the answer is graded."},
		"difficulty":  {Type: "string", Enum: []string{"beginner", "intermediate", "advanced", "exam_scenarios"}, Description: "Difficulty classification (required)."},
		"options": {
			Type: "array",
			Items: &InputSchemaProperty{
				Type: "object",
				Properties: map[string]*InputSchemaProperty{
					"key":         {Type: "string", Description: "Single-letter answer key (A-Z)."},
					"text":        {Type: "string", Description: "Option text."},
					"explanation": {Type: "string", Description: "Explanation revealed after the answer is graded."},
					"is_correct":  {Type: "boolean", Description: "Set true for every correct option (one or more)."},
					"position":    {Type: "integer", Description: "1-based render order."},
				},
				Required: []string{"key", "text", "explanation", "is_correct", "position"},
			},
			Description: "Multiple-choice options. At least two are required with at least one marked correct (multi-answer questions allowed).",
		},
		"references": {
			Type: "array",
			Items: &InputSchemaProperty{
				Type: "object",
				Properties: map[string]*InputSchemaProperty{
					"title":    {Type: "string", Description: "Reference label."},
					"url":      {Type: "string", Description: "Optional URL."},
					"citation": {Type: "string", Description: "Optional citation text."},
					"position": {Type: "integer", Description: "1-based render order."},
				},
				Required: []string{"title", "position"},
			},
			Description: "Optional reference list rendered below the question.",
		},
	}
	if requireID {
		props["id"] = &InputSchemaProperty{Type: "string", Description: "Question UUID (required)."}
	}
	return props
}

func decodeQuestionInput(raw json.RawMessage, requireID bool) (exams.Question, map[string]string, error) {
	fields := make(map[string]string)
	var payload struct {
		ID          string            `json:"id"`
		ExamID      string            `json:"exam_id"`
		DomainID    string            `json:"domain_id"`
		Prompt      string            `json:"prompt"`
		Scenario    string            `json:"scenario"`
		Explanation string            `json:"explanation"`
		Difficulty  exams.Difficulty  `json:"difficulty"`
		Position    int               `json:"position"`
		Options     []exams.Option    `json:"options"`
		References  []exams.Reference `json:"references"`
	}
	if len(raw) == 0 {
		return exams.Question{}, map[string]string{"arguments": "is required"}, nil
	}
	if err := decodeStrict(raw, &payload); err != nil {
		return exams.Question{}, nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
	}
	question := exams.Question{
		ID:          payload.ID,
		ExamID:      payload.ExamID,
		DomainID:    payload.DomainID,
		Prompt:      payload.Prompt,
		Scenario:    payload.Scenario,
		Explanation: payload.Explanation,
		Difficulty:  payload.Difficulty,
		Position:    payload.Position,
		Options:     payload.Options,
		References:  payload.References,
	}
	if requireID && question.ID == "" {
		fields["id"] = "is required"
	}
	if question.ExamID == "" {
		fields["exam_id"] = "is required"
	}
	if question.DomainID == "" {
		fields["domain_id"] = "is required"
	}
	if question.Prompt == "" {
		fields["prompt"] = "is required"
	}
	return question, fields, nil
}

func mapQuestionToolError(tool string, err error) error {
	var fe exams.FieldErrors
	if errors.As(err, &fe) {
		return NewToolError("invalid_args", "question input failed validation", fe)
	}
	if errors.Is(err, store.ErrArchivedQuestionCannotPatch) || strings.Contains(err.Error(), "archived question cannot be patched") {
		return NewToolError("invalid_state", "archived questions cannot be patched; duplicate or restore the exam first", map[string]string{"id": "exam is archived"})
	}
	if errors.Is(err, store.ErrQuestionNotFoundInSnapshot) || strings.Contains(err.Error(), "question not found in snapshot") {
		return NewToolError("internal_error", "question snapshot is inconsistent", nil)
	}
	if errors.Is(err, store.ErrQuestionNotFound) || strings.Contains(err.Error(), "question not found") {
		return NewToolError("not_found", "question not found", map[string]string{"id": "not found"})
	}
	if strings.Contains(err.Error(), "violates foreign key") && strings.Contains(err.Error(), "domain") {
		return NewToolError("invalid_args", "domain does not exist or is inactive", map[string]string{"domain_id": "does not exist"})
	}
	return fmt.Errorf("%s: %w", tool, err)
}

// errExamNotFound is retained for callers that prefer the local sentinel; the
// canonical error lives in the store package.
var _ = store.ErrPublishedExamNotFound
