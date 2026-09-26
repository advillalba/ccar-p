package publishing

import "context"

// CacheInvalidator receives targeted invalidation requests once a publication
// transaction commits successfully. Implementations MUST be safe to call after
// the database transaction has committed because failures here are not
// expected to roll back the publication.
type CacheInvalidator interface {
	Invalidate(ctx context.Context, target InvalidationTarget) error
}

// InvalidationTarget identifies the cached representation that should be
// purged. Multiple targets can be flushed for a single publication so the
// service returns the resolved list on PublicationResult.CachesFlushed.
type InvalidationTarget struct {
	EntityType EntityType
	EntityID   string
	Slug       string
	Version    int
}

// Label produces a stable label for logging and assertions.
func (t InvalidationTarget) Label() string {
	switch t.EntityType {
	case EntityNote:
		return "note:" + t.Slug
	case EntityExam:
		return "exam:" + t.Slug
	default:
		return string(t.EntityType)
	}
}

// MultiCacheInvalidator fans invalidation calls out to every registered
// backend. Failures from one backend are aggregated but do not abort the
// remaining ones; the returned error is the first non-nil error encountered.
type MultiCacheInvalidator struct {
	Backends []CacheInvalidator
}

// Invalidate dispatches the target to every backend in registration order.
func (m *MultiCacheInvalidator) Invalidate(ctx context.Context, target InvalidationTarget) error {
	for _, backend := range m.Backends {
		if backend == nil {
			continue
		}
		if err := backend.Invalidate(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

// RecordingCacheInvalidator captures every invalidation request for tests.
type RecordingCacheInvalidator struct {
	Targets []InvalidationTarget
}

// NewRecordingCacheInvalidator creates an empty recording invalidator.
func NewRecordingCacheInvalidator() *RecordingCacheInvalidator {
	return &RecordingCacheInvalidator{Targets: []InvalidationTarget{}}
}

// Invalidate records the target and returns success.
func (r *RecordingCacheInvalidator) Invalidate(_ context.Context, target InvalidationTarget) error {
	r.Targets = append(r.Targets, target)
	return nil
}

// Snapshot returns the captured targets for assertions.
func (r *RecordingCacheInvalidator) Snapshot() []InvalidationTarget {
	out := make([]InvalidationTarget, len(r.Targets))
	copy(out, r.Targets)
	return out
}
