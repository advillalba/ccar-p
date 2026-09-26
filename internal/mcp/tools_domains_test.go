package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type domainStub struct {
	domains         []content.Domain
	includeInactive bool
	created         content.DomainInput
	updatedID       string
	updated         content.DomainInput
	deletedID       string
	err             error
}

func (s *domainStub) Domains(_ context.Context, includeInactive bool) ([]content.Domain, error) {
	s.includeInactive = includeInactive
	return s.domains, s.err
}
func (s *domainStub) DomainByID(context.Context, string) (content.Domain, error) {
	if s.err != nil {
		return content.Domain{}, s.err
	}
	return s.domains[0], nil
}
func (s *domainStub) CreateDomain(_ context.Context, input content.DomainInput) (content.Domain, error) {
	s.created = input
	return content.Domain{ID: "domain-id", Name: input.Name}, s.err
}
func (s *domainStub) UpdateDomainFull(_ context.Context, id string, input content.DomainInput) (content.Domain, error) {
	s.updatedID, s.updated = id, input
	return content.Domain{ID: id, Name: input.Name}, s.err
}
func (s *domainStub) DeleteDomain(_ context.Context, id string) error {
	s.deletedID = id
	return s.err
}

func TestRegisterDomainTools(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterDomainTools(registry, &domainStub{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_domains", "get_domain", "create_domain", "update_domain", "delete_domain"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("tool %s was not registered", name)
		}
		wantScope := ScopeDomainsRead
		if name == "create_domain" || name == "update_domain" || name == "delete_domain" {
			wantScope = ScopeDomainsWrite
		}
		if len(tool.RequiredScopes) != 1 || tool.RequiredScopes[0] != wantScope {
			t.Fatalf("tool %s has scopes %v", name, tool.RequiredScopes)
		}
	}
}

func TestDomainToolCRUD(t *testing.T) {
	stub := &domainStub{domains: []content.Domain{{ID: "domain-id", Name: "Existing"}}}
	registry := NewRegistry()
	if err := RegisterDomainTools(registry, stub); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Ctx: context.Background()}

	input := json.RawMessage(`{"name":"Risk","slug":"risk","description":"Risk domain","weight":25,"sort_order":1,"is_active":true}`)
	if _, err := registryTool(t, registry, "create_domain").Handler(ctx, input); err != nil {
		t.Fatal(err)
	}
	if stub.created.Name != "Risk" || stub.created.SortOrder != 1 {
		t.Fatalf("unexpected create input: %+v", stub.created)
	}

	update := json.RawMessage(`{"id":"domain-id","name":"Market Risk","slug":"market-risk","description":"Updated","weight":30,"sort_order":1,"is_active":false}`)
	if _, err := registryTool(t, registry, "update_domain").Handler(ctx, update); err != nil {
		t.Fatal(err)
	}
	if stub.updatedID != "domain-id" || stub.updated.Name != "Market Risk" || stub.updated.IsActive {
		t.Fatalf("unexpected update input: %s %+v", stub.updatedID, stub.updated)
	}

	if _, err := registryTool(t, registry, "delete_domain").Handler(ctx, json.RawMessage(`{"id":"domain-id"}`)); err != nil {
		t.Fatal(err)
	}
	if stub.deletedID != "domain-id" {
		t.Fatalf("unexpected deleted id: %s", stub.deletedID)
	}
}

func TestDomainToolsRejectInvalidInput(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterDomainTools(registry, &domainStub{}); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Ctx: context.Background()}
	_, err := registryTool(t, registry, "create_domain").Handler(ctx, json.RawMessage(`{"name":"","slug":"Bad Slug","weight":101,"sort_order":0,"is_active":true,"unknown":true}`))
	var toolError *ToolError
	if !errors.As(err, &toolError) || toolError.Code != "invalid_args" {
		t.Fatalf("expected invalid_args, got %v", err)
	}
}

func TestMapDomainToolError(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{pgx.ErrNoRows, "domain_not_found"},
		{store.ErrDomainInUse, "domain_in_use"},
		{&pgconn.PgError{Code: "23503"}, "domain_in_use"},
		{&pgconn.PgError{Code: "23505"}, "domain_conflict"},
	}
	for _, test := range tests {
		var toolError *ToolError
		if err := mapDomainToolError("domain", test.err); !errors.As(err, &toolError) || toolError.Code != test.code {
			t.Fatalf("expected %s for %v, got %v", test.code, test.err, err)
		}
	}
}

func registryTool(t *testing.T, registry *Registry, name string) *Tool {
	t.Helper()
	tool, ok := registry.Get(name)
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	return tool
}
