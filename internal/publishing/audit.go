package publishing

import (
	"context"
	"encoding/json"
	"time"
)

// AuditEvent is the structured record persisted to publication_events. The
// fields mirror the database columns exactly so adapters can perform direct
// inserts without translation.
type AuditEvent struct {
	ID           string
	EntityType   EntityType
	EntityID     string
	Version      int
	Action       Action
	ActorKind    ActorKind
	ActorID      string
	Reason       string
	Metadata     map[string]any
	DryRun       bool
	ExportStatus ExportOutcome
	Outcome      string
	CreatedAt    time.Time
}

// PublicationFilter narrows the results returned by ListPublications. All
// fields are optional; zero values mean "no constraint".
type PublicationFilter struct {
	EntityType EntityType
	EntityID   string
	Action     Action
	ActorID    string
	From       time.Time
	To         time.Time
	Limit      int
	Offset     int
}

// AuditRepository is the integration point with the future audit/repository
// package. It MUST be implemented by the persistence adapter; the publishing
// service treats it as an opaque dependency so callers can swap it.
type AuditRepository interface {
	RecordPublication(ctx context.Context, event AuditEvent) (AuditReceipt, error)
	ListPublications(ctx context.Context, filter PublicationFilter) ([]AuditEvent, error)
}

// AuditReceipt is returned by RecordPublication so the publishing service can
// surface the database identifier to the caller when appropriate.
type AuditReceipt struct {
	ID        string
	CreatedAt time.Time
}

// RecordingAuditRepository is an in-memory AuditRepository used by tests and
// as the foundation for the future postgres-backed implementation. It stores
// events in insertion order so tests can assert on the recorded history.
type RecordingAuditRepository struct {
	Events []AuditEvent
}

// NewRecordingAuditRepository creates an empty recording audit repository.
func NewRecordingAuditRepository() *RecordingAuditRepository {
	return &RecordingAuditRepository{Events: []AuditEvent{}}
}

// RecordPublication appends the event and returns a synthetic receipt. The
// synthetic receipt identifier is mirrored onto the recorded event so callers
// that re-list events see a stable ID without needing to thread the receipt
// through their own bookkeeping.
func (r *RecordingAuditRepository) RecordPublication(_ context.Context, event AuditEvent) (AuditReceipt, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	receiptID := syntheticAuditID(len(r.Events) + 1)
	event.ID = receiptID
	r.Events = append(r.Events, event)
	return AuditReceipt{ID: receiptID, CreatedAt: event.CreatedAt}, nil
}

// Snapshot returns a defensive copy of the recorded events.
func (r *RecordingAuditRepository) Snapshot() []AuditEvent {
	out := make([]AuditEvent, len(r.Events))
	copy(out, r.Events)
	return out
}

// ListPublications returns the recorded events that match the supplied filter.
// The implementation applies entity, action, actor, and time-bound predicates
// in order before applying limit and offset. Tests rely on it to exercise the
// admin audit history without a real database.
func (r *RecordingAuditRepository) ListPublications(_ context.Context, filter PublicationFilter) ([]AuditEvent, error) {
	out := make([]AuditEvent, 0)
	for _, event := range r.Events {
		if !publicationMatches(event, filter) {
			continue
		}
		out = append(out, event)
	}
	if filter.Offset > 0 && filter.Offset < len(out) {
		out = out[filter.Offset:]
	} else if filter.Offset >= len(out) {
		return []AuditEvent{}, nil
	}
	if filter.Limit > 0 && filter.Limit < len(out) {
		out = out[:filter.Limit]
	}
	return out, nil
}

func publicationMatches(event AuditEvent, filter PublicationFilter) bool {
	if filter.EntityType != "" && event.EntityType != filter.EntityType {
		return false
	}
	if filter.Action != "" && event.Action != filter.Action {
		return false
	}
	if filter.ActorID != "" && event.ActorID != filter.ActorID {
		return false
	}
	if !filter.From.IsZero() && event.CreatedAt.Before(filter.From) {
		return false
	}
	if !filter.To.IsZero() && event.CreatedAt.After(filter.To) {
		return false
	}
	return true
}

func syntheticAuditID(seq int) string {
	id := make([]byte, 16)
	// Encode sequence number into the high-order bytes so successive events
	// produce distinct synthetic IDs without relying on UUID generation.
	id[0] = byte(seq >> 24)
	id[1] = byte(seq >> 16)
	id[2] = byte(seq >> 8)
	id[3] = byte(seq)
	return hexEncode(id)
}

func hexEncode(raw []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, b := range raw {
		out[i*2] = hexDigits[b>>4]
		out[i*2+1] = hexDigits[b&0x0f]
	}
	return string(out)
}

// MarshalMetadata renders metadata for jsonb storage. The helper centralizes
// encoding so callers do not need to import encoding/json directly.
func MarshalMetadata(metadata map[string]any) []byte {
	if len(metadata) == 0 {
		return []byte("{}")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}
