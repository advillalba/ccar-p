package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPublishedNoteNotFound = errors.New("published note not found")
	ErrDomainInUse           = errors.New("domain is in use")
)

type ContentRepository struct {
	store *Store
}

func NewContentRepository(store *Store) *ContentRepository {
	return &ContentRepository{store: store}
}

func (r *ContentRepository) ActiveDomains(ctx context.Context) ([]content.Domain, error) {
	return r.Domains(ctx, false)
}

func (r *ContentRepository) Domains(ctx context.Context, includeInactive bool) ([]content.Domain, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	rows, err := r.store.Pool.Query(ctx, `SELECT id, name, slug, description, weight, sort_order, is_active FROM domains WHERE is_active OR $1 ORDER BY sort_order, id`, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := []content.Domain{}
	for rows.Next() {
		var domain content.Domain
		if err := rows.Scan(&domain.ID, &domain.Name, &domain.Slug, &domain.Description, &domain.Weight, &domain.SortOrder, &domain.IsActive); err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

func (r *ContentRepository) DomainByID(ctx context.Context, id string) (content.Domain, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var domain content.Domain
	err := r.store.Pool.QueryRow(ctx, `SELECT id, name, slug, description, weight, sort_order, is_active FROM domains WHERE id = $1`, id).Scan(&domain.ID, &domain.Name, &domain.Slug, &domain.Description, &domain.Weight, &domain.SortOrder, &domain.IsActive)
	return domain, err
}

func (r *ContentRepository) CreateDomain(ctx context.Context, input content.DomainInput) (content.Domain, error) {
	if err := input.Validate(); err != nil {
		return content.Domain{}, err
	}
	var domain content.Domain
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('domains'))`); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM domains`).Scan(&count); err != nil {
			return err
		}
		if input.SortOrder > count+1 {
			return fmt.Errorf("sort order must be between 1 and %d", count+1)
		}
		if _, err := tx.Exec(ctx, `UPDATE domains SET sort_order = sort_order + 1000 WHERE sort_order >= $1`, input.SortOrder); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE domains SET sort_order = sort_order - 999 WHERE sort_order >= $1`, input.SortOrder+1000); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO domains (name, slug, description, weight, sort_order, is_active) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, name, slug, description, weight, sort_order, is_active`, strings.TrimSpace(input.Name), strings.TrimSpace(input.Slug), strings.TrimSpace(input.Description), input.Weight, input.SortOrder, input.IsActive).Scan(&domain.ID, &domain.Name, &domain.Slug, &domain.Description, &domain.Weight, &domain.SortOrder, &domain.IsActive)
	})
	return domain, err
}

func (r *ContentRepository) UpdateDomainFull(ctx context.Context, id string, input content.DomainInput) (content.Domain, error) {
	if id == "" {
		return content.Domain{}, fmt.Errorf("domain is required")
	}
	if err := input.Validate(); err != nil {
		return content.Domain{}, err
	}
	var domain content.Domain
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('domains'))`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM domains ORDER BY sort_order FOR UPDATE`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var domainID string
			if err := rows.Scan(&domainID); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, domainID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if input.SortOrder > len(ids) {
			return fmt.Errorf("sort order must be between 1 and %d", len(ids))
		}
		current := -1
		for i, domainID := range ids {
			if domainID == id {
				current = i
				break
			}
		}
		if current < 0 {
			return pgx.ErrNoRows
		}
		ids = append(ids[:current], ids[current+1:]...)
		target := input.SortOrder - 1
		ids = append(ids, "")
		copy(ids[target+1:], ids[target:])
		ids[target] = id
		if _, err := tx.Exec(ctx, `UPDATE domains SET sort_order = sort_order + 1000`); err != nil {
			return err
		}
		for position, domainID := range ids {
			if _, err := tx.Exec(ctx, `UPDATE domains SET sort_order = $1 WHERE id = $2`, position+1, domainID); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `UPDATE domains SET name = $1, slug = $2, description = $3, weight = $4, is_active = $5 WHERE id = $6 RETURNING id, name, slug, description, weight, sort_order, is_active`, strings.TrimSpace(input.Name), strings.TrimSpace(input.Slug), strings.TrimSpace(input.Description), input.Weight, input.IsActive, id).Scan(&domain.ID, &domain.Name, &domain.Slug, &domain.Description, &domain.Weight, &domain.SortOrder, &domain.IsActive)
	})
	return domain, err
}

func (r *ContentRepository) DeleteDomain(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("domain is required")
	}
	return r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('domains'))`); err != nil {
			return err
		}
		var sortOrder int
		if err := tx.QueryRow(ctx, `DELETE FROM domains WHERE id = $1 RETURNING sort_order`, id).Scan(&sortOrder); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE domains SET sort_order = sort_order - 1 WHERE sort_order > $1`, sortOrder)
		return err
	})
}

func (r *ContentRepository) UpdateDomain(ctx context.Context, id string, weight, sortOrder int, active bool) (content.Domain, error) {
	if id == "" || weight < 0 || weight > 100 || sortOrder < 1 {
		return content.Domain{}, fmt.Errorf("invalid domain update")
	}
	var domain content.Domain
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('domains'))`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM domains ORDER BY sort_order FOR UPDATE`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var domainID string
			if err := rows.Scan(&domainID); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, domainID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if sortOrder > len(ids) {
			return fmt.Errorf("sort order must be between 1 and %d", len(ids))
		}
		current := -1
		for i, domainID := range ids {
			if domainID == id {
				current = i
				break
			}
		}
		if current < 0 {
			return pgx.ErrNoRows
		}
		ids = append(ids[:current], ids[current+1:]...)
		target := sortOrder - 1
		ids = append(ids, "")
		copy(ids[target+1:], ids[target:])
		ids[target] = id
		if _, err := tx.Exec(ctx, `UPDATE domains SET sort_order = sort_order + 1000`); err != nil {
			return err
		}
		for position, domainID := range ids {
			if _, err := tx.Exec(ctx, `UPDATE domains SET sort_order = $1 WHERE id = $2`, position+1, domainID); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `UPDATE domains SET weight = $1, is_active = $2 WHERE id = $3 RETURNING id, name, slug, weight, sort_order, is_active`, weight, active, id).Scan(&domain.ID, &domain.Name, &domain.Slug, &domain.Weight, &domain.SortOrder, &domain.IsActive)
	})
	return domain, err
}

func (r *ContentRepository) CreateNote(ctx context.Context, authorID string, input content.NoteInput) (content.Note, error) {
	if authorID == "" {
		return content.Note{}, fmt.Errorf("author is required")
	}
	if err := input.Validate(); err != nil {
		return content.Note{}, err
	}
	var note content.Note
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDomain(ctx, tx, input.DomainID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO notes (domain_id, author_id, title, slug, summary, markdown, reading_time_minutes) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, domain_id, author_id, title, slug, summary, markdown, status, version, reading_time_minutes, published_at, created_at, updated_at`, input.DomainID, authorID, strings.TrimSpace(input.Title), input.Slug, strings.TrimSpace(input.Summary), input.Markdown, content.EstimateReadingTime(input.Markdown)).Scan(&note.ID, &note.DomainID, &note.AuthorID, &note.Title, &note.Slug, &note.Summary, &note.Markdown, &note.Status, &note.Version, &note.ReadingTimeMinutes, &note.PublishedAt, &note.CreatedAt, &note.UpdatedAt); err != nil {
			return err
		}
		return replaceNoteRelations(ctx, tx, note.ID, input.Tags, input.References)
	})
	if err != nil {
		return content.Note{}, err
	}
	note.Tags, note.References = input.Tags, input.References
	return note, nil
}

func (r *ContentRepository) UpdateNote(ctx context.Context, id, authorID string, input content.NoteInput) (content.Note, error) {
	if id == "" || authorID == "" {
		return content.Note{}, fmt.Errorf("note and author are required")
	}
	if err := input.Validate(); err != nil {
		return content.Note{}, err
	}
	var note content.Note
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDomain(ctx, tx, input.DomainID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE notes SET domain_id = $1, title = $2, slug = $3, summary = $4, markdown = $5, reading_time_minutes = $6 WHERE id = $7 AND author_id = $8 AND status = 'draft' RETURNING id, domain_id, author_id, title, slug, summary, markdown, status, version, reading_time_minutes, published_at, created_at, updated_at`, input.DomainID, strings.TrimSpace(input.Title), input.Slug, strings.TrimSpace(input.Summary), input.Markdown, content.EstimateReadingTime(input.Markdown), id, authorID).Scan(&note.ID, &note.DomainID, &note.AuthorID, &note.Title, &note.Slug, &note.Summary, &note.Markdown, &note.Status, &note.Version, &note.ReadingTimeMinutes, &note.PublishedAt, &note.CreatedAt, &note.UpdatedAt); err != nil {
			return err
		}
		return replaceNoteRelations(ctx, tx, note.ID, input.Tags, input.References)
	})
	if err != nil {
		return content.Note{}, err
	}
	note.Tags, note.References = input.Tags, input.References
	return note, nil
}

func replaceNoteRelations(ctx context.Context, tx pgx.Tx, noteID string, tags []content.TagInput, references []content.ReferenceInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM note_tags WHERE note_id = $1`, noteID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM note_references WHERE note_id = $1`, noteID); err != nil {
		return err
	}
	for _, tag := range tags {
		name := strings.ToLower(strings.TrimSpace(tag.Name))
		var tagID string
		if err := tx.QueryRow(ctx, `INSERT INTO tags (name, slug) VALUES ($1, $2) ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name RETURNING id`, name, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO note_tags (note_id, tag_id) VALUES ($1, $2)`, noteID, tagID); err != nil {
			return err
		}
	}
	for _, reference := range references {
		if _, err := tx.Exec(ctx, `INSERT INTO note_references (note_id, title, url, citation, position) VALUES ($1, $2, $3, $4, $5)`, noteID, strings.TrimSpace(reference.Title), nullableString(reference.URL), strings.TrimSpace(reference.Citation), reference.Position); err != nil {
			return err
		}
	}
	return nil
}

func (r *ContentRepository) ListPublishedNotes(ctx context.Context) ([]content.PublishedNoteSummary, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	rows, err := r.store.Pool.Query(ctx, `SELECT n.id, n.domain_id, n.title, n.slug, n.summary, n.reading_time_minutes, n.published_at,
		ARRAY(SELECT t.name FROM note_tags nt JOIN tags t ON t.id = nt.tag_id WHERE nt.note_id = n.id ORDER BY t.name)
		FROM notes n WHERE n.status = 'published' ORDER BY n.published_at DESC, n.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := []content.PublishedNoteSummary{}
	for rows.Next() {
		var note content.PublishedNoteSummary
		var tags []string
		if err := rows.Scan(&note.ID, &note.DomainID, &note.Title, &note.Slug, &note.Summary, &note.ReadingTimeMinutes, &note.PublishedAt, &tags); err != nil {
			return nil, err
		}
		for _, tag := range tags {
			note.Tags = append(note.Tags, content.TagInput{Name: tag})
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

func (r *ContentRepository) PublishedNoteBySlug(ctx context.Context, slug string) (content.PublishedNote, error) {
	if slug == "" {
		return content.PublishedNote{}, ErrPublishedNoteNotFound
	}
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var note content.PublishedNote
	err := r.store.Pool.QueryRow(ctx, `SELECT id, domain_id, title, slug, summary, markdown, reading_time_minutes, published_at, updated_at, version FROM notes WHERE status = 'published' AND slug = $1`, slug).Scan(&note.ID, &note.DomainID, &note.Title, &note.Slug, &note.Summary, &note.Markdown, &note.ReadingTimeMinutes, &note.PublishedAt, &note.UpdatedAt, &note.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return content.PublishedNote{}, ErrPublishedNoteNotFound
		}
		return content.PublishedNote{}, err
	}
	tagRows, err := r.store.Pool.Query(ctx, `SELECT t.name FROM note_tags nt JOIN tags t ON t.id = nt.tag_id JOIN notes n ON n.id = nt.note_id WHERE n.id = $1 ORDER BY t.name`, note.ID)
	if err != nil {
		return content.PublishedNote{}, err
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var name string
		if err := tagRows.Scan(&name); err != nil {
			return content.PublishedNote{}, err
		}
		note.Tags = append(note.Tags, content.TagInput{Name: name})
	}
	if err := tagRows.Err(); err != nil {
		return content.PublishedNote{}, err
	}
	refRows, err := r.store.Pool.Query(ctx, `SELECT title, COALESCE(url, ''), citation, position FROM note_references WHERE note_id = $1 ORDER BY position`, note.ID)
	if err != nil {
		return content.PublishedNote{}, err
	}
	defer refRows.Close()
	for refRows.Next() {
		var ref content.ReferenceInput
		if err := refRows.Scan(&ref.Title, &ref.URL, &ref.Citation, &ref.Position); err != nil {
			return content.PublishedNote{}, err
		}
		note.References = append(note.References, ref)
	}
	if err := refRows.Err(); err != nil {
		return content.PublishedNote{}, err
	}
	return note, nil
}

func (r *ContentRepository) AdjacentPublishedNotes(ctx context.Context, publishedAt time.Time) (content.AdjacentNote, content.AdjacentNote, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var prev content.AdjacentNote
	var next content.AdjacentNote
	err := r.store.Pool.QueryRow(ctx, `SELECT slug, title FROM notes WHERE status = 'published' AND published_at < $1 ORDER BY published_at DESC LIMIT 1`, publishedAt).Scan(&prev.Slug, &prev.Title)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return prev, next, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		prev = content.AdjacentNote{}
	}
	err = r.store.Pool.QueryRow(ctx, `SELECT slug, title FROM notes WHERE status = 'published' AND published_at > $1 ORDER BY published_at ASC LIMIT 1`, publishedAt).Scan(&next.Slug, &next.Title)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return prev, next, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		next = content.AdjacentNote{}
	}
	return prev, next, nil
}

func (r *ContentRepository) storeQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.store.QueryTimeout
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func requireActiveDomain(ctx context.Context, db DBTX, id string) error {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM domains WHERE id = $1 AND is_active)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return content.FieldErrors{"domain_id": "must identify an active domain"}
	}
	return nil
}

// Publish transitions a draft note to published, bumps its version, and
// stamps published_at. The caller is responsible for taking the row lock via
// LoadForUpdate and recording the publication event so the audit history
// stays consistent with the persisted state.
func (r *ContentRepository) Publish(ctx context.Context, id, _ string, version int) (content.Note, error) {
	var note content.Note
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var currentVersion int
		var status content.Status
		if err := tx.QueryRow(ctx, `SELECT version, status FROM notes WHERE id = $1 FOR UPDATE`, id).Scan(&currentVersion, &status); err != nil {
			return err
		}
		if status != content.StatusDraft {
			return fmt.Errorf("cannot publish note in %s state", status)
		}
		if version != currentVersion+1 {
			return errors.New("stale prospective note version")
		}
		return tx.QueryRow(ctx, `UPDATE notes SET status = 'published', version = $2, published_at = now() WHERE id = $1 AND status = 'draft' RETURNING id, domain_id, author_id, title, slug, summary, markdown, status, version, reading_time_minutes, published_at, created_at, updated_at`, id, version).Scan(&note.ID, &note.DomainID, &note.AuthorID, &note.Title, &note.Slug, &note.Summary, &note.Markdown, &note.Status, &note.Version, &note.ReadingTimeMinutes, &note.PublishedAt, &note.CreatedAt, &note.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return content.Note{}, fmt.Errorf("note %q not found", id)
	}
	return note, err
}

// Archive transitions a note to archived. The caller is responsible for the
// row lock and validation.
func (r *ContentRepository) Archive(ctx context.Context, id, _ string) (content.Note, error) {
	var note content.Note
	err := r.store.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status content.Status
		if err := tx.QueryRow(ctx, `SELECT status FROM notes WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return err
		}
		if status != content.StatusDraft && status != content.StatusPublished {
			return fmt.Errorf("cannot archive note in %s state", status)
		}
		return tx.QueryRow(ctx, `UPDATE notes SET status = 'archived' WHERE id = $1 AND status = $2 RETURNING id, domain_id, author_id, title, slug, summary, markdown, status, version, reading_time_minutes, published_at, created_at, updated_at`, id, status).Scan(&note.ID, &note.DomainID, &note.AuthorID, &note.Title, &note.Slug, &note.Summary, &note.Markdown, &note.Status, &note.Version, &note.ReadingTimeMinutes, &note.PublishedAt, &note.CreatedAt, &note.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return content.Note{}, fmt.Errorf("note %q not found", id)
	}
	return note, err
}

func (r *ContentRepository) LoadForUpdate(ctx context.Context, id string) (content.Note, error) {
	return r.NoteByID(ctx, id)
}

func (r *ContentRepository) LoadBySlug(ctx context.Context, slug string) (content.Note, error) {
	var id string
	if err := r.store.Pool.QueryRow(ctx, `SELECT id FROM notes WHERE slug = $1`, slug).Scan(&id); err != nil {
		return content.Note{}, err
	}
	return r.NoteByID(ctx, id)
}
