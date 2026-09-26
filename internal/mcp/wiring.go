package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/publishing"
	"github.com/ccar-p/study-platform/internal/store"
)

const Route = "/mcp"

type ProductionOptions struct {
	Store                    *store.Store
	Publishing               *publishing.Service
	BootstrapTokenHash       string
	BootstrapContentAuthorID string
	BootstrapScopes          Scopes
	Logger                   *slog.Logger
}

func NewProductionHandler(options ProductionOptions) (http.Handler, error) {
	if options.Store == nil || options.Publishing == nil {
		return nil, errors.New("mcp: store and publishing service are required")
	}
	tokens := NewPostgresTokenRepository(options.Store)
	authenticator, err := NewTokenAuthenticatorWithRepository(options.BootstrapTokenHash, tokens, options.BootstrapScopes, options.BootstrapContentAuthorID)
	if err != nil {
		return nil, err
	}
	contentRepository := store.NewContentRepository(options.Store)
	examRepository := store.NewExamRepository(options.Store)
	questionRepository := store.NewQuestionRepository(options.Store)
	optionRepository := store.NewOptionRepository(options.Store)
	auditRepository := NewPostgresAuditRepository(options.Store)
	registry := NewRegistry()
	if err := RegisterDomainTools(registry, contentRepository); err != nil {
		return nil, err
	}
	if err := RegisterNotesTools(registry, newNotesAdapter(contentRepository), options.Publishing); err != nil {
		return nil, err
	}
	if err := RegisterExamsTools(registry, newExamsAdapter(examRepository), questionRepository, newOptionsAdapter(optionRepository), options.Publishing); err != nil {
		return nil, err
	}
	publicationAudit := store.NewStoreAuditRepository(options.Store)
	if err := RegisterPublicationTools(registry, publicationAudit); err != nil {
		return nil, err
	}
	if err := RegisterAuditTools(registry, auditRepository); err != nil {
		return nil, err
	}
	return NewServer(registry, authenticator, options.Logger, WithAuditRepository(auditRepository)).Handler(), nil
}

type notesAdapter struct{ *store.ContentRepository }

func newNotesAdapter(repository *store.ContentRepository) *notesAdapter {
	return &notesAdapter{repository}
}

func (a *notesAdapter) NoteByID(ctx context.Context, id string) (content.Note, error) {
	return a.ContentRepository.NoteByID(ctx, id)
}

type examsAdapter struct{ *store.ExamRepository }

func newExamsAdapter(repository *store.ExamRepository) *examsAdapter {
	return &examsAdapter{repository}
}

type optionsAdapter struct{ *store.OptionRepository }

func newOptionsAdapter(repository *store.OptionRepository) *optionsAdapter {
	return &optionsAdapter{repository}
}
