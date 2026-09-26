package store

import (
	"context"
	"errors"
	"time"

	"github.com/ccar-p/study-platform/internal/content"
	"github.com/jackc/pgx/v5"
)

// NoteSummary is a lightweight projection used by the admin list view. It
// includes the lifecycle status so administrators can distinguish drafts from
// published or archived entries without re-fetching the full record.
type NoteSummary struct {
	ID                 string     `json:"id"`
	DomainID           string     `json:"domain_id"`
	Title              string     `json:"title"`
	Slug               string     `json:"slug"`
	Status             string     `json:"status"`
	Version            int        `json:"version"`
	ReadingTimeMinutes int        `json:"reading_time_minutes"`
	PublishedAt        *time.Time `json:"published_at,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// NoteByID returns the full record for any note (draft, published, archived).
// It is intended for the admin workspace where every state must be editable.
func (r *ContentRepository) NoteByID(ctx context.Context, id string) (content.Note, error) {
	if id == "" {
		return content.Note{}, ErrPublishedNoteNotFound
	}
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	var note content.Note
	err := r.store.Pool.QueryRow(ctx, `SELECT id, domain_id, author_id, title, slug, summary, markdown, status, version, reading_time_minutes, published_at, created_at, updated_at FROM notes WHERE id = $1`, id).Scan(&note.ID, &note.DomainID, &note.AuthorID, &note.Title, &note.Slug, &note.Summary, &note.Markdown, &note.Status, &note.Version, &note.ReadingTimeMinutes, &note.PublishedAt, &note.CreatedAt, &note.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return content.Note{}, ErrPublishedNoteNotFound
		}
		return content.Note{}, err
	}
	if err := r.loadNoteTags(ctx, id, &note); err != nil {
		return content.Note{}, err
	}
	if err := r.loadNoteReferences(ctx, id, &note); err != nil {
		return content.Note{}, err
	}
	return note, nil
}

// ListAllNotes returns summaries for every note regardless of status. The
// caller is expected to apply pagination before exposing the data to the UI.
func (r *ContentRepository) ListAllNotes(ctx context.Context) ([]NoteSummary, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	rows, err := r.store.Pool.Query(ctx, `SELECT id, domain_id, title, slug, status, version, reading_time_minutes, published_at, updated_at FROM notes ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteSummary, 0)
	for rows.Next() {
		var summary NoteSummary
		if err := rows.Scan(&summary.ID, &summary.DomainID, &summary.Title, &summary.Slug, &summary.Status, &summary.Version, &summary.ReadingTimeMinutes, &summary.PublishedAt, &summary.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

// DeleteNote removes a draft note by id. Notes that are already published or
// archived must be archived first; this preserves the audit trail of any
// public version.
func (r *ContentRepository) DeleteNote(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("note id is required")
	}
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	cmd, err := r.store.Pool.Exec(ctx, `DELETE FROM notes WHERE id = $1 AND status = 'draft'`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("draft note not found")
	}
	return nil
}

func (r *ContentRepository) loadNoteTags(ctx context.Context, noteID string, note *content.Note) error {
	rows, err := r.store.Pool.Query(ctx, `SELECT t.name FROM note_tags nt JOIN tags t ON t.id = nt.tag_id WHERE nt.note_id = $1 ORDER BY t.name`, noteID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		note.Tags = append(note.Tags, content.TagInput{Name: name})
	}
	return rows.Err()
}

func (r *ContentRepository) loadNoteReferences(ctx context.Context, noteID string, note *content.Note) error {
	rows, err := r.store.Pool.Query(ctx, `SELECT title, COALESCE(url, ''), citation, position FROM note_references WHERE note_id = $1 ORDER BY position`, noteID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ref content.ReferenceInput
		if err := rows.Scan(&ref.Title, &ref.URL, &ref.Citation, &ref.Position); err != nil {
			return err
		}
		note.References = append(note.References, ref)
	}
	return rows.Err()
}

// AdminPublishNote is the admin-facing publication helper used by the
// publishing service. The repository handles the row lock, state check,
// version bump, and audit-friendly updated_at timestamp; the caller is
// responsible for emitting the publication_event.
func (r *ContentRepository) AdminPublishNote(ctx context.Context, id, actorID string, version int) (content.Note, error) {
	return r.Publish(ctx, id, actorID, version)
}

// AdminArchiveNote mirrors AdminPublishNote for archive transitions. The
// implementation intentionally returns an error until the publishing
// service is integrated with the admin transport.
func (r *ContentRepository) AdminArchiveNote(ctx context.Context, id, actorID string) (content.Note, error) {
	return r.Archive(ctx, id, actorID)
}
