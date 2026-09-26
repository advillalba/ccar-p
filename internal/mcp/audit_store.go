package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type MCPAuditRecord struct {
	ID             string         `json:"id"`
	ServiceTokenID string         `json:"service_token_id"`
	Tool           string         `json:"tool"`
	TargetType     string         `json:"target_type,omitempty"`
	TargetID       string         `json:"target_id,omitempty"`
	Outcome        string         `json:"outcome"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
}

type MCPAuditFilter struct {
	ServiceTokenID string
	Tool           string
	TargetType     string
	TargetID       string
	Outcome        string
	From           time.Time
	To             time.Time
	Limit          int
	Offset         int
}

type MCPAuditStore interface {
	AuditRepository
	ListMCPAudit(context.Context, MCPAuditFilter) ([]MCPAuditRecord, error)
}

type PostgresAuditRepository struct {
	db TokenDB
}

func NewPostgresAuditRepository(db TokenDB) *PostgresAuditRepository {
	return &PostgresAuditRepository{db: db}
}

func (r *PostgresAuditRepository) RecordMCPAudit(ctx context.Context, event AuditEvent) error {
	metadata, _ := json.Marshal(map[string]any{"actor_name": event.Actor.Name})
	var targetID any
	if event.TargetID != "" {
		targetID = event.TargetID
	}
	var tokenID any = event.Actor.ID
	if event.Actor.ID == bootstrapTokenID {
		tokenID = nil
	}
	_, err := r.db.Exec(ctx, `INSERT INTO mcp_audit_events (service_token_id, actor_id, tool, target_type, target_id, outcome, metadata, request_id, error_code, duration_ms, created_at) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7::jsonb, $8, $9, $10, $11)`, tokenID, event.Actor.ID, event.Tool, event.TargetType, targetID, event.Outcome, metadata, event.RequestID, event.ErrorCode, event.Duration.Milliseconds(), event.CreatedAt)
	return err
}

func (r *PostgresAuditRepository) ListMCPAudit(ctx context.Context, filter MCPAuditFilter) ([]MCPAuditRecord, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || filter.Offset < 0 {
		return nil, errors.New("mcp: invalid audit pagination")
	}
	rows, err := r.query(ctx, filter, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]MCPAuditRecord, 0)
	for rows.Next() {
		var item MCPAuditRecord
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.ServiceTokenID, &item.Tool, &item.TargetType, &item.TargetID, &item.Outcome, &metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metadata, &item.Metadata)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresAuditRepository) query(ctx context.Context, filter MCPAuditFilter, limit int) (pgx.Rows, error) {
	return r.db.Query(ctx, `SELECT id, actor_id, tool, COALESCE(target_type, ''), COALESCE(target_id::text, ''), outcome, metadata, created_at FROM mcp_audit_events WHERE ($1 = '' OR actor_id = $1) AND ($2 = '' OR tool = $2) AND ($3 = '' OR target_type = $3) AND ($4 = '' OR target_id::text = $4) AND ($5 = '' OR outcome = $5) AND ($6::timestamptz IS NULL OR created_at >= $6) AND ($7::timestamptz IS NULL OR created_at <= $7) ORDER BY created_at DESC, id DESC LIMIT $8 OFFSET $9`, filter.ServiceTokenID, filter.Tool, filter.TargetType, filter.TargetID, filter.Outcome, nullableAuditTime(filter.From), nullableAuditTime(filter.To), limit, filter.Offset)
}

func nullableAuditTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
