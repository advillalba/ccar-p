package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ccar-p/study-platform/internal/publishing"
)

// PublicationStatusAPI surfaces the historical query needed by the
// get_publication_status tool. The production implementation is the
// publishing.AuditRepository persisted to publication_events; the recording
// stub in tests satisfies the same interface.
type PublicationStatusAPI interface {
	ListPublications(ctx context.Context, filter publishing.PublicationFilter) ([]publishing.AuditEvent, error)
}

// PublicationTools bundles the dependencies shared by the publication tools.
type PublicationTools struct {
	Audit PublicationStatusAPI
}

// RegisterPublicationTools installs the get_publication_status tool.
func RegisterPublicationTools(registry *Registry, audit PublicationStatusAPI) error {
	if registry == nil {
		return errors.New("mcp: registry is required")
	}
	if audit == nil {
		return errors.New("mcp: audit repository is required")
	}
	tools := &PublicationTools{Audit: audit}
	return registry.Register(tools.toolGetPublicationStatus())
}

func (p *PublicationTools) toolGetPublicationStatus() *Tool {
	return &Tool{
		Name:           "get_publication_status",
		Description:    "Look up the latest publication_event entries matching the supplied filter.",
		RequiredScopes: []Scope{ScopeAudit},
		InputSchema: InputSchema{
			Properties: map[string]*InputSchemaProperty{
				"entity_id":   {Type: "string", Description: "Entity UUID to filter by."},
				"entity_type": {Type: "string", Enum: []string{"note", "exam"}, Description: "Entity kind."},
				"action":      {Type: "string", Enum: []string{"publish", "archive"}, Description: "Publication action."},
				"actor_id":    {Type: "string", Description: "Restrict to events performed by the supplied actor id."},
				"limit":       {Type: "integer", Description: "Maximum number of events to return (default 25)."},
				"offset":      {Type: "integer", Description: "Number of events to skip from the start of the result set."},
				"from":        {Type: "string", Description: "RFC3339 timestamp; events before this are excluded."},
				"to":          {Type: "string", Description: "RFC3339 timestamp; events after this are excluded."},
			},
		},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			filter, fields, err := decodePublicationFilter(raw)
			if err != nil {
				return nil, err
			}
			if len(fields) > 0 {
				return nil, NewToolError("invalid_args", "filter failed validation", fields)
			}
			events, err := p.Audit.ListPublications(ctx.Ctx, filter)
			if err != nil {
				return nil, fmt.Errorf("get_publication_status: %w", err)
			}
			return map[string]any{
				"events": events,
				"filter": filter,
			}, nil
		},
	}
}

func decodePublicationFilter(raw json.RawMessage) (publishing.PublicationFilter, map[string]string, error) {
	fields := make(map[string]string)
	filter := publishing.PublicationFilter{}
	if len(raw) == 0 {
		return filter, fields, nil
	}
	var args struct {
		EntityID   string `json:"entity_id"`
		EntityType string `json:"entity_type"`
		Action     string `json:"action"`
		ActorID    string `json:"actor_id"`
		Limit      int    `json:"limit"`
		Offset     int    `json:"offset"`
		From       string `json:"from"`
		To         string `json:"to"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return filter, nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
	}
	if args.EntityID != "" {
		filter.EntityID = args.EntityID
	}
	switch args.EntityType {
	case "":
		// no constraint
	case "note":
		filter.EntityType = publishing.EntityNote
	case "exam":
		filter.EntityType = publishing.EntityExam
	default:
		fields["entity_type"] = "must be \"note\" or \"exam\""
	}
	switch args.Action {
	case "":
		// no constraint
	case "publish":
		filter.Action = publishing.ActionPublish
	case "archive":
		filter.Action = publishing.ActionArchive
	default:
		fields["action"] = "must be \"publish\" or \"archive\""
	}
	if args.ActorID != "" {
		filter.ActorID = args.ActorID
	}
	if args.Limit < 0 || args.Limit > 200 {
		fields["limit"] = "must be between 0 and 200"
	}
	if args.Offset < 0 {
		fields["offset"] = "must be non-negative"
	}
	filter.Limit = args.Limit
	filter.Offset = args.Offset
	if args.From != "" {
		from, err := time.Parse(time.RFC3339, args.From)
		if err != nil {
			fields["from"] = "must be an RFC3339 timestamp"
		} else {
			filter.From = from
		}
	}
	if args.To != "" {
		to, err := time.Parse(time.RFC3339, args.To)
		if err != nil {
			fields["to"] = "must be an RFC3339 timestamp"
		} else {
			filter.To = to
		}
	}
	return filter, fields, nil
}
