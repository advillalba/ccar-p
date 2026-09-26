package store

import (
	"context"
	"time"

	"github.com/ccar-p/study-platform/internal/exams"
)

// ExamSummary is a lightweight projection used by the admin list view. It
// exposes the lifecycle state, version, and question count so administrators
// can quickly triage large exam catalogues without loading every question.
type ExamSummary struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Slug             string     `json:"slug"`
	Difficulty       string     `json:"difficulty"`
	Status           string     `json:"status"`
	Version          int        `json:"version"`
	TimeLimitMinutes int        `json:"time_limit_minutes"`
	PassPercentage   float64    `json:"pass_percentage"`
	QuestionCount    int        `json:"question_count"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ListAllExams returns summaries for every exam regardless of status. The
// caller is expected to apply pagination before exposing the data.
func (r *ExamRepository) ListAllExams(ctx context.Context) ([]ExamSummary, error) {
	ctx, cancel := r.storeQueryContext(ctx)
	defer cancel()
	rows, err := r.store.Pool.Query(ctx, `SELECT e.id, e.title, e.slug, e.difficulty, e.status, e.version, e.time_limit_minutes, e.pass_percentage, COALESCE(q.count, 0), e.published_at, e.updated_at FROM exams e LEFT JOIN (SELECT exam_id, count(*) AS count FROM questions GROUP BY exam_id) q ON q.exam_id = e.id ORDER BY e.updated_at DESC, e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ExamSummary, 0)
	for rows.Next() {
		var summary ExamSummary
		if err := rows.Scan(&summary.ID, &summary.Title, &summary.Slug, &summary.Difficulty, &summary.Status, &summary.Version, &summary.TimeLimitMinutes, &summary.PassPercentage, &summary.QuestionCount, &summary.PublishedAt, &summary.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

// AdminPublishExam is the admin-facing publication helper used by the
// publishing service. The repository handles the row lock, snapshot
// persistence, and version bump; the caller is responsible for emitting
// the publication_event.
func (r *ExamRepository) AdminPublishExam(ctx context.Context, id, actorID string, version int) (exams.Snapshot, error) {
	return r.PublishVersion(ctx, id, actorID, version)
}

// AdminArchiveExam mirrors AdminPublishExam for archive transitions. The
// implementation intentionally returns an error until the publishing
// service is integrated with the admin transport.
func (r *ExamRepository) AdminArchiveExam(ctx context.Context, id, actorID string) (exams.Snapshot, error) {
	return r.ArchiveVersion(ctx, id, actorID)
}
