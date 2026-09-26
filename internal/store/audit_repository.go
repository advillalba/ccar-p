package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/publishing"
)

// PublicationEventRecord mirrors the publication_events table so the store
// layer can map query results into the publishing.AuditEvent shape without
// scattering column lists across the codebase.
type PublicationEventRecord struct {
	ID           string
	EntityType   string
	EntityID     string
	Version      int
	Action       string
	ActorKind    string
	ActorID      string
	Reason       string
	Metadata     []byte
	DryRun       bool
	ExportStatus string
	Outcome      string
	CreatedAt    time.Time
}

// StoreAuditRepository persists publication events into the publication_events
// table and supports paginated listing for the admin audit history. The
// implementation deliberately keeps column names local to this file so future
// migrations can adjust the schema with a single point of change.
type StoreAuditRepository struct {
	store *Store
}

// NewStoreAuditRepository returns an audit repository bound to the given store.
func NewStoreAuditRepository(store *Store) *StoreAuditRepository {
	return &StoreAuditRepository{store: store}
}

// RecordPublication inserts the event and returns the database identifier.
func (r *StoreAuditRepository) RecordPublication(ctx context.Context, event publishing.AuditEvent) (publishing.AuditReceipt, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	metadata := publishing.MarshalMetadata(event.Metadata)
	var id string
	var createdAt time.Time
	err := r.store.Pool.QueryRow(ctx, `INSERT INTO publication_events (entity_type, entity_id, version, action, actor_type, actor_id, reason, metadata, dry_run, export_status, outcome, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, COALESCE($12, now())) RETURNING id, created_at`,
		string(event.EntityType), event.EntityID, event.Version, string(event.Action), string(event.ActorKind), event.ActorID, event.Reason, metadata, event.DryRun, string(event.ExportStatus), event.Outcome, nullableTime(event.CreatedAt),
	).Scan(&id, &createdAt)
	if err != nil {
		return publishing.AuditReceipt{}, fmt.Errorf("insert publication event: %w", err)
	}
	return publishing.AuditReceipt{ID: id, CreatedAt: createdAt}, nil
}

// ListPublications applies the supplied filter and returns matching events in
// reverse chronological order. Limit defaults to 25 when zero and is capped at
// 200 to keep responses bounded.
func (r *StoreAuditRepository) ListPublications(ctx context.Context, filter publishing.PublicationFilter) ([]publishing.AuditEvent, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	clauses := make([]string, 0)
	args := make([]any, 0)
	idx := 1
	if filter.EntityType != "" {
		clauses = append(clauses, fmt.Sprintf("entity_type = $%d", idx))
		args = append(args, string(filter.EntityType))
		idx++
	}
	if filter.Action != "" {
		clauses = append(clauses, fmt.Sprintf("action = $%d", idx))
		args = append(args, string(filter.Action))
		idx++
	}
	if filter.ActorID != "" {
		clauses = append(clauses, fmt.Sprintf("actor_id = $%d", idx))
		args = append(args, filter.ActorID)
		idx++
	}
	if !filter.From.IsZero() {
		clauses = append(clauses, fmt.Sprintf("created_at >= $%d", idx))
		args = append(args, filter.From)
		idx++
	}
	if !filter.To.IsZero() {
		clauses = append(clauses, fmt.Sprintf("created_at <= $%d", idx))
		args = append(args, filter.To)
		idx++
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, entity_type, entity_id, version, action, actor_type, actor_id, reason, metadata, dry_run, export_status, outcome, created_at FROM publication_events`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT %d OFFSET %d", limit, offset)
	rows, err := r.store.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]publishing.AuditEvent, 0)
	for rows.Next() {
		record, err := scanPublicationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, auditEventFromRecord(record))
	}
	return out, rows.Err()
}

func scanPublicationEvent(rows interface {
	Scan(dest ...any) error
}) (PublicationEventRecord, error) {
	var record PublicationEventRecord
	if err := rows.Scan(&record.ID, &record.EntityType, &record.EntityID, &record.Version, &record.Action, &record.ActorKind, &record.ActorID, &record.Reason, &record.Metadata, &record.DryRun, &record.ExportStatus, &record.Outcome, &record.CreatedAt); err != nil {
		return PublicationEventRecord{}, err
	}
	return record, nil
}

func auditEventFromRecord(record PublicationEventRecord) publishing.AuditEvent {
	metadata := decodeMetadata(record.Metadata)
	return publishing.AuditEvent{
		ID:           record.ID,
		EntityType:   publishing.EntityType(record.EntityType),
		EntityID:     record.EntityID,
		Version:      record.Version,
		Action:       publishing.Action(record.Action),
		ActorKind:    publishing.ActorKind(record.ActorKind),
		ActorID:      record.ActorID,
		Reason:       record.Reason,
		Metadata:     metadata,
		DryRun:       record.DryRun,
		ExportStatus: publishing.ExportOutcome(record.ExportStatus),
		Outcome:      record.Outcome,
		CreatedAt:    record.CreatedAt,
	}
}

func decodeMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

// storeQueryContext applies the configured query timeout to ctx and returns
// the derived context plus its cancellation function. It mirrors the helpers
// exposed on the content and exam repositories so the audit repository shares
// the same operational envelope.
func (r *StoreAuditRepository) storeQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.store.QueryTimeout
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}
