package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/ccar-p/study-platform/internal/store"
)

type PublicNoteSummaryDTO struct {
	ID                 string       `json:"id"`
	Slug               string       `json:"slug"`
	Title              string       `json:"title"`
	Summary            string       `json:"summary"`
	Domain             PublicDomain `json:"domain"`
	Tags               []PublicTag  `json:"tags"`
	ReadingTimeMinutes int          `json:"reading_time_minutes"`
	PublishedAt        time.Time    `json:"published_at"`
}

type PublicNoteDetailDTO struct {
	ID                 string              `json:"id"`
	Slug               string              `json:"slug"`
	Title              string              `json:"title"`
	Summary            string              `json:"summary"`
	Domain             PublicDomain        `json:"domain"`
	Tags               []PublicTag         `json:"tags"`
	References         []PublicReference   `json:"references"`
	HTML               string              `json:"html"`
	TOC                []content.TOCItem   `json:"toc"`
	ReadingTimeMinutes int                 `json:"reading_time_minutes"`
	PublishedAt        time.Time           `json:"published_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	Version            int                 `json:"version"`
	Previous           *PublicAdjacentNote `json:"previous"`
	Next               *PublicAdjacentNote `json:"next"`
}

type PublicDomain struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Weight    int    `json:"weight"`
	SortOrder int    `json:"sort_order"`
}

type PublicTag struct {
	Name string `json:"name"`
}

type PublicReference struct {
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	Citation string `json:"citation,omitempty"`
	Position int    `json:"position"`
}

type PublicAdjacentNote struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type PublicDomainListDTO struct {
	Domains []PublicDomain `json:"domains"`
}

type PublicNoteListDTO struct {
	Notes []PublicNoteSummaryDTO `json:"notes"`
}

type PublicNoteResponse struct {
	Note PublicNoteDetailDTO `json:"note"`
}

type ContentReader interface {
	ActiveDomains(ctx context.Context) ([]content.Domain, error)
	ListPublishedNotes(ctx context.Context) ([]content.PublishedNoteSummary, error)
	PublishedNoteBySlug(ctx context.Context, slug string) (content.PublishedNote, error)
	AdjacentPublishedNotes(ctx context.Context, publishedAt time.Time) (content.AdjacentNote, content.AdjacentNote, error)
}

type NotesHandler struct {
	reader ContentReader
}

func NewNotesHandler(reader ContentReader) *NotesHandler {
	return &NotesHandler{reader: reader}
}

func (h *NotesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/domains", h.listDomains)
	mux.HandleFunc("GET /api/v1/notes", h.listNotes)
	mux.HandleFunc("GET /api/v1/notes/{slug}", h.getNoteBySlug)
}

func (h *NotesHandler) listDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := h.reader.ActiveDomains(r.Context())
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	out := PublicDomainListDTO{Domains: make([]PublicDomain, 0, len(domains))}
	for _, domain := range domains {
		out.Domains = append(out.Domains, PublicDomain{
			ID:        domain.ID,
			Slug:      domain.Slug,
			Name:      domain.Name,
			Weight:    domain.Weight,
			SortOrder: domain.SortOrder,
		})
	}
	WriteData(w, r, http.StatusOK, out)
}

func (h *NotesHandler) listNotes(w http.ResponseWriter, r *http.Request) {
	notes, err := h.reader.ListPublishedNotes(r.Context())
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	domainsByID, err := h.domainsByID(r.Context())
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	out := PublicNoteListDTO{Notes: make([]PublicNoteSummaryDTO, 0, len(notes))}
	for _, note := range notes {
		summary := PublicNoteSummaryDTO{
			ID:                 note.ID,
			Slug:               note.Slug,
			Title:              note.Title,
			Summary:            note.Summary,
			Tags:               summarizeTags(note.Tags),
			ReadingTimeMinutes: note.ReadingTimeMinutes,
			PublishedAt:        note.PublishedAt,
		}
		if domain, ok := domainsByID[note.DomainID]; ok {
			summary.Domain = PublicDomain{ID: domain.ID, Slug: domain.Slug, Name: domain.Name, Weight: domain.Weight, SortOrder: domain.SortOrder}
		}
		out.Notes = append(out.Notes, summary)
	}
	WriteData(w, r, http.StatusOK, out)
}

func (h *NotesHandler) getNoteBySlug(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		WriteError(w, r, http.StatusBadRequest, "invalid_slug", "note slug is required", nil)
		return
	}
	note, err := h.reader.PublishedNoteBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrPublishedNoteNotFound) {
			WriteError(w, r, http.StatusNotFound, "note_not_found", "note not found", nil)
			return
		}
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	domainsByID, err := h.domainsByID(r.Context())
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	prev, next, err := h.reader.AdjacentPublishedNotes(r.Context(), note.PublishedAt)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	document, renderedHTML, err := content.RenderHTML(note.Markdown)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "render_failed", "published note could not be rendered", nil)
		return
	}
	detail := PublicNoteDetailDTO{
		ID:                 note.ID,
		Slug:               note.Slug,
		Title:              note.Title,
		Summary:            note.Summary,
		Tags:               summarizeTags(note.Tags),
		References:         summarizeReferences(note.References),
		HTML:               renderedHTML,
		TOC:                document.TOC,
		ReadingTimeMinutes: note.ReadingTimeMinutes,
		PublishedAt:        note.PublishedAt,
		UpdatedAt:          note.UpdatedAt,
		Version:            note.Version,
	}
	if domain, ok := domainsByID[note.DomainID]; ok {
		detail.Domain = PublicDomain{ID: domain.ID, Slug: domain.Slug, Name: domain.Name, Weight: domain.Weight, SortOrder: domain.SortOrder}
	}
	if prev.Slug != "" {
		detail.Previous = &PublicAdjacentNote{Slug: prev.Slug, Title: prev.Title}
	}
	if next.Slug != "" {
		detail.Next = &PublicAdjacentNote{Slug: next.Slug, Title: next.Title}
	}
	WriteData(w, r, http.StatusOK, PublicNoteResponse{Note: detail})
}

func (h *NotesHandler) domainsByID(ctx context.Context) (map[string]content.Domain, error) {
	domains, err := h.reader.ActiveDomains(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]content.Domain, len(domains))
	for _, domain := range domains {
		byID[domain.ID] = domain
	}
	return byID, nil
}

func summarizeTags(tags []content.TagInput) []PublicTag {
	if len(tags) == 0 {
		return []PublicTag{}
	}
	out := make([]PublicTag, 0, len(tags))
	for _, tag := range tags {
		out = append(out, PublicTag{Name: tag.Name})
	}
	return out
}

func summarizeReferences(refs []content.ReferenceInput) []PublicReference {
	if len(refs) == 0 {
		return []PublicReference{}
	}
	out := make([]PublicReference, 0, len(refs))
	for _, ref := range refs {
		out = append(out, PublicReference{Title: ref.Title, URL: ref.URL, Citation: ref.Citation, Position: ref.Position})
	}
	return out
}
