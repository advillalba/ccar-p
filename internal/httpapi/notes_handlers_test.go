package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/store"
)

type stubContentReader struct {
	domains     []content.Domain
	notes       []content.PublishedNoteSummary
	notesBySlug map[string]content.PublishedNote
	adjacent    map[time.Time]struct {
		prev content.AdjacentNote
		next content.AdjacentNote
	}
	errBySlug map[string]error
}

func newStubContentReader() *stubContentReader {
	return &stubContentReader{
		notesBySlug: make(map[string]content.PublishedNote),
		adjacent: make(map[time.Time]struct {
			prev content.AdjacentNote
			next content.AdjacentNote
		}),
		errBySlug: make(map[string]error),
	}
}

func (s *stubContentReader) ActiveDomains(_ context.Context) ([]content.Domain, error) {
	out := make([]content.Domain, len(s.domains))
	copy(out, s.domains)
	return out, nil
}

func (s *stubContentReader) ListPublishedNotes(_ context.Context) ([]content.PublishedNoteSummary, error) {
	out := make([]content.PublishedNoteSummary, len(s.notes))
	copy(out, s.notes)
	return out, nil
}

func (s *stubContentReader) PublishedNoteBySlug(_ context.Context, slug string) (content.PublishedNote, error) {
	if err, ok := s.errBySlug[slug]; ok {
		return content.PublishedNote{}, err
	}
	if note, ok := s.notesBySlug[slug]; ok {
		return note, nil
	}
	return content.PublishedNote{}, store.ErrPublishedNoteNotFound
}

func (s *stubContentReader) AdjacentPublishedNotes(_ context.Context, publishedAt time.Time) (content.AdjacentNote, content.AdjacentNote, error) {
	pair, ok := s.adjacent[publishedAt]
	if !ok {
		return content.AdjacentNote{}, content.AdjacentNote{}, nil
	}
	return pair.prev, pair.next, nil
}

func newNotesHandler() (*NotesHandler, *stubContentReader) {
	reader := newStubContentReader()
	reader.domains = []content.Domain{
		{ID: "domain-cloud", Name: "Cloud Concepts", Slug: "cloud-concepts", Weight: 27, SortOrder: 1},
		{ID: "domain-sec", Name: "Security and Compliance", Slug: "security-and-compliance", Weight: 18, SortOrder: 2},
	}
	publishedAt := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	reader.notes = []content.PublishedNoteSummary{
		{ID: "note-1", DomainID: "domain-cloud", Title: "Shared Responsibility Overview", Slug: "shared-responsibility", Summary: "Summary text.", Tags: []content.TagInput{{Name: "cloud"}}, ReadingTimeMinutes: 3, PublishedAt: publishedAt},
	}
	reader.notesBySlug["shared-responsibility"] = content.PublishedNote{
		ID:                 "note-1",
		DomainID:           "domain-cloud",
		Title:              "Shared Responsibility Overview",
		Slug:               "shared-responsibility",
		Summary:            "Summary text.",
		Markdown:           "# Heading\n\nParagraph.",
		ReadingTimeMinutes: 3,
		PublishedAt:        publishedAt,
		UpdatedAt:          publishedAt,
		Version:            1,
		Tags:               []content.TagInput{{Name: "cloud"}},
		References:         []content.ReferenceInput{{Title: "Reference A", URL: "https://example.com", Citation: "Citation A", Position: 1}},
	}
	reader.adjacent[publishedAt] = struct {
		prev content.AdjacentNote
		next content.AdjacentNote
	}{
		prev: content.AdjacentNote{Slug: "earlier-note", Title: "Earlier Note"},
		next: content.AdjacentNote{Slug: "later-note", Title: "Later Note"},
	}
	return NewNotesHandler(reader), reader
}

func TestDomainsEndpointReturnsActiveDomainsInOrder(t *testing.T) {
	handler, _ := newNotesHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var envelope successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PublicDomainListDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid domain list: %s", string(raw))
	}
	if len(dto.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(dto.Domains))
	}
	if dto.Domains[0].Slug != "cloud-concepts" || dto.Domains[1].Slug != "security-and-compliance" {
		t.Fatalf("domains returned out of order: %#v", dto.Domains)
	}
	for _, domain := range dto.Domains {
		if domain.Weight < 0 {
			t.Fatalf("domain weight must be non-negative: %#v", domain)
		}
	}
}

func TestNotesListEndpointReturnsPublishedOnly(t *testing.T) {
	handler, _ := newNotesHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	disallowed := []string{"is_correct", "correct_option_id", "author_id", "explanation", "markdown"}
	for _, term := range disallowed {
		if strings.Contains(strings.ToLower(body), term) {
			t.Fatalf("response leaked %q: %s", term, body)
		}
	}
	var envelope successEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", body)
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PublicNoteListDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid note list: %s", string(raw))
	}
	if len(dto.Notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(dto.Notes))
	}
	note := dto.Notes[0]
	if note.Slug != "shared-responsibility" {
		t.Fatalf("unexpected slug: %s", note.Slug)
	}
	if note.Domain.Slug != "cloud-concepts" {
		t.Fatalf("domain not resolved: %#v", note.Domain)
	}
	if note.PublishedAt.IsZero() {
		t.Fatalf("published_at missing")
	}
	if len(note.Tags) != 1 || note.Tags[0].Name != "cloud" {
		t.Fatalf("tags missing: %#v", note.Tags)
	}
}

func TestNotesListHidesDraftsAndArchived(t *testing.T) {
	handler, reader := newNotesHandler()
	reader.errBySlug["draft"] = store.ErrPublishedNoteNotFound
	reader.notesBySlug["draft"] = content.PublishedNote{
		ID:          "draft-id",
		DomainID:    "domain-cloud",
		Title:       "Hidden Draft",
		Slug:        "draft",
		Summary:     "should never appear",
		Markdown:    "private content",
		PublishedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notes/draft", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for draft, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Hidden Draft") || strings.Contains(w.Body.String(), "private content") {
		t.Fatalf("draft content leaked: %s", w.Body.String())
	}
}

func TestNoteBySlugReturnsSafeDetail(t *testing.T) {
	handler, _ := newNotesHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notes/shared-responsibility", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	disallowed := []string{"is_correct", "correct_option", "author_id", "status"}
	for _, term := range disallowed {
		if strings.Contains(strings.ToLower(body), term) {
			t.Fatalf("note detail leaked %q: %s", term, body)
		}
	}
	var envelope successEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("invalid envelope: %s", body)
	}
	raw, _ := json.Marshal(envelope.Data)
	var dto PublicNoteResponse
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("invalid note response: %s", string(raw))
	}
	if dto.Note.Previous == nil || dto.Note.Previous.Slug != "earlier-note" {
		t.Fatalf("previous note missing or wrong: %#v", dto.Note.Previous)
	}
	if dto.Note.Next == nil || dto.Note.Next.Slug != "later-note" {
		t.Fatalf("next note missing or wrong: %#v", dto.Note.Next)
	}
	if dto.Note.HTML == "" {
		t.Fatalf("rendered body content missing")
	}
	if strings.Contains(strings.ToLower(body), `"markdown"`) {
		t.Fatalf("raw markdown leaked: %s", body)
	}
	if len(dto.Note.Tags) != 1 || dto.Note.Tags[0].Name != "cloud" {
		t.Fatalf("tags missing: %#v", dto.Note.Tags)
	}
	if len(dto.Note.References) != 1 || dto.Note.References[0].Title != "Reference A" {
		t.Fatalf("references missing: %#v", dto.Note.References)
	}
	if dto.Note.Domain.Slug != "cloud-concepts" {
		t.Fatalf("domain not resolved: %#v", dto.Note.Domain)
	}
}

func TestNoteBySlugMissingReturns404(t *testing.T) {
	handler, reader := newNotesHandler()
	reader.errBySlug["missing"] = store.ErrPublishedNoteNotFound
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notes/missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "note_not_found" {
		t.Fatalf("expected note_not_found error, got %s", w.Body.String())
	}
}

func TestNoteBySlugSurfacesInternalErrors(t *testing.T) {
	handler, reader := newNotesHandler()
	reader.errBySlug["oops"] = errors.New("database unavailable")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notes/oops", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d %s", w.Code, w.Body.String())
	}
}

func TestNotesHandlerImplementsExpectedShape(t *testing.T) {
	var _ ContentReader = (*stubContentReader)(nil)
	var _ http.Handler = http.NewServeMux()
}
