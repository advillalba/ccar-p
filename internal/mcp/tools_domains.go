package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DomainsAPI interface {
	Domains(ctx context.Context, includeInactive bool) ([]content.Domain, error)
	DomainByID(ctx context.Context, id string) (content.Domain, error)
	CreateDomain(ctx context.Context, input content.DomainInput) (content.Domain, error)
	UpdateDomainFull(ctx context.Context, id string, input content.DomainInput) (content.Domain, error)
	DeleteDomain(ctx context.Context, id string) error
}

type domainTools struct {
	domains DomainsAPI
}

func RegisterDomainTools(registry *Registry, domains DomainsAPI) error {
	if registry == nil {
		return errors.New("mcp: registry is required")
	}
	if domains == nil {
		return errors.New("mcp: domains repository is required")
	}
	tools := &domainTools{domains: domains}
	for _, tool := range []*Tool{tools.toolListDomains(), tools.toolGetDomain(), tools.toolCreateDomain(), tools.toolUpdateDomain(), tools.toolDeleteDomain()} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func (t *domainTools) toolListDomains() *Tool {
	return &Tool{
		Name:           "list_domains",
		Description:    "List content domains ordered by sort_order, optionally including inactive domains.",
		RequiredScopes: []Scope{ScopeDomainsRead},
		InputSchema: InputSchema{Properties: map[string]*InputSchemaProperty{
			"include_inactive": {Type: "boolean", Description: "Include inactive domains."},
		}},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var args struct {
				IncludeInactive bool `json:"include_inactive"`
			}
			if err := decodeStrict(raw, &args); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
			}
			domains, err := t.domains.Domains(ctx.Ctx, args.IncludeInactive)
			if err != nil {
				return nil, fmt.Errorf("list_domains: %w", err)
			}
			return map[string]any{"domains": domains}, nil
		},
	}
}

func (t *domainTools) toolGetDomain() *Tool {
	return &Tool{
		Name:           "get_domain",
		Description:    "Fetch a content domain by UUID.",
		RequiredScopes: []Scope{ScopeDomainsRead},
		InputSchema: InputSchema{Properties: map[string]*InputSchemaProperty{
			"id": {Type: "string", Description: "Domain UUID (required)."},
		}, Required: []string{"id"}},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			id, err := decodeDomainID(raw)
			if err != nil {
				return nil, err
			}
			domain, err := t.domains.DomainByID(ctx.Ctx, id)
			if err != nil {
				return nil, mapDomainToolError("get_domain", err)
			}
			return domain, nil
		},
	}
}

func (t *domainTools) toolCreateDomain() *Tool {
	return &Tool{
		Name:           "create_domain",
		Description:    "Create a content domain and insert it at the requested sort position.",
		RequiredScopes: []Scope{ScopeDomainsWrite},
		InputSchema:    domainInputSchema(),
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			input, err := decodeDomainInput(raw)
			if err != nil {
				return nil, err
			}
			domain, err := t.domains.CreateDomain(ctx.Ctx, input)
			if err != nil {
				return nil, mapDomainToolError("create_domain", err)
			}
			return domain, nil
		},
	}
}

func (t *domainTools) toolUpdateDomain() *Tool {
	properties := domainInputSchema().Properties
	properties["id"] = &InputSchemaProperty{Type: "string", Description: "Domain UUID (required)."}
	return &Tool{
		Name:           "update_domain",
		Description:    "Update a content domain and move it to the requested sort position.",
		RequiredScopes: []Scope{ScopeDomainsWrite},
		InputSchema:    InputSchema{Properties: properties, Required: []string{"id", "name", "slug", "weight", "sort_order", "is_active"}},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID string `json:"id"`
				content.DomainInput
			}
			if err := decodeStrict(raw, &args); err != nil {
				return nil, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
			}
			if args.ID == "" {
				return nil, NewToolError("invalid_args", "domain input failed validation", map[string]string{"id": "is required"})
			}
			if err := args.DomainInput.Validate(); err != nil {
				return nil, mapDomainToolError("update_domain", err)
			}
			domain, err := t.domains.UpdateDomainFull(ctx.Ctx, args.ID, args.DomainInput)
			if err != nil {
				return nil, mapDomainToolError("update_domain", err)
			}
			return domain, nil
		},
	}
}

func (t *domainTools) toolDeleteDomain() *Tool {
	return &Tool{
		Name:           "delete_domain",
		Description:    "Delete an unreferenced content domain.",
		RequiredScopes: []Scope{ScopeDomainsWrite},
		InputSchema: InputSchema{Properties: map[string]*InputSchemaProperty{
			"id": {Type: "string", Description: "Domain UUID (required)."},
		}, Required: []string{"id"}},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			id, err := decodeDomainID(raw)
			if err != nil {
				return nil, err
			}
			if err := t.domains.DeleteDomain(ctx.Ctx, id); err != nil {
				return nil, mapDomainToolError("delete_domain", err)
			}
			return map[string]any{"id": id, "deleted": true}, nil
		},
	}
}

func domainInputSchema() InputSchema {
	return InputSchema{
		Properties: map[string]*InputSchemaProperty{
			"name":        {Type: "string", Description: "Display name (required)."},
			"slug":        {Type: "string", Description: "Lowercase slug (required)."},
			"description": {Type: "string", Description: "Domain description."},
			"weight":      {Type: "integer", Description: "Relative exam weight from 0 to 100 (required)."},
			"sort_order":  {Type: "integer", Description: "One-based display position (required)."},
			"is_active":   {Type: "boolean", Description: "Whether the domain is available for content (required)."},
		},
		Required: []string{"name", "slug", "weight", "sort_order", "is_active"},
	}
}

func decodeDomainID(raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return "", NewToolError("invalid_args", "arguments must be a JSON object with id", map[string]string{"arguments": err.Error()})
	}
	if args.ID == "" {
		return "", NewToolError("invalid_args", "id is required", map[string]string{"id": "is required"})
	}
	return args.ID, nil
}

func decodeDomainInput(raw json.RawMessage) (content.DomainInput, error) {
	var input content.DomainInput
	if err := decodeStrict(raw, &input); err != nil {
		return input, NewToolError("invalid_args", "arguments must be a JSON object", map[string]string{"arguments": err.Error()})
	}
	if err := input.Validate(); err != nil {
		return input, mapDomainToolError("create_domain", err)
	}
	return input, nil
}

func mapDomainToolError(tool string, err error) error {
	var fields content.FieldErrors
	if errors.As(err, &fields) {
		return NewToolError("invalid_args", "domain input failed validation", fields)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return NewToolError("domain_not_found", "domain does not exist", nil)
	}
	if errors.Is(err, store.ErrDomainInUse) {
		return NewToolError("domain_in_use", "domain is referenced by content and cannot be deleted", nil)
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return NewToolError("domain_in_use", "domain is referenced by content and cannot be deleted", nil)
		case "23505":
			return NewToolError("domain_conflict", "domain name, slug, or sort order already exists", nil)
		}
	}
	return fmt.Errorf("%s: %w", tool, err)
}
