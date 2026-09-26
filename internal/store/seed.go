package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ccar-p/study-platform/internal/auth"
	"github.com/jackc/pgx/v5"
)

type SeedAdmin struct{ Email, Password, DisplayName string }

func (s *Store) SeedDevelopment(ctx context.Context, appEnv string, admin SeedAdmin) error {
	if appEnv != "development" {
		return errors.New("development seeds are disabled outside development")
	}
	admin.Email = strings.ToLower(strings.TrimSpace(admin.Email))
	if admin.Email == "" || admin.DisplayName == "" {
		return errors.New("SEED_ADMIN_EMAIL and SEED_ADMIN_DISPLAY_NAME are required")
	}
	hash, err := auth.HashPassword(admin.Password)
	if err != nil {
		return fmt.Errorf("invalid SEED_ADMIN_PASSWORD: %w", err)
	}
	return s.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var adminID string
		err := tx.QueryRow(ctx, `INSERT INTO users (email, display_name, password_hash, role) VALUES ($1,$2,$3,'admin') ON CONFLICT ((lower(email))) DO UPDATE SET display_name=EXCLUDED.display_name, password_hash=EXCLUDED.password_hash, role='admin', is_active=true RETURNING id`, admin.Email, admin.DisplayName, hash).Scan(&adminID)
		if err != nil {
			return err
		}
		domains := []struct {
			name, slug    string
			weight, order int
		}{
			{"Cloud Concepts", "cloud-concepts", 27, 101}, {"Security and Compliance", "security-and-compliance", 18, 102}, {"Technology", "technology", 20, 103}, {"Billing and Pricing", "billing-and-pricing", 20, 104}, {"Cloud Architecture", "cloud-architecture", 15, 105},
		}
		ids := make([]string, len(domains))
		for i, domain := range domains {
			if err := tx.QueryRow(ctx, `INSERT INTO domains (name,slug,description,weight,sort_order) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name, weight=EXCLUDED.weight, sort_order=EXCLUDED.sort_order RETURNING id`, domain.name, domain.slug, "Development study domain", domain.weight, domain.order).Scan(&ids[i]); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO notes (domain_id,author_id,title,slug,summary,markdown,status,version,reading_time_minutes,published_at) VALUES ($1,$2,$3,$4,$5,$6,'published',1,3,now()) ON CONFLICT (slug) DO UPDATE SET domain_id=EXCLUDED.domain_id, author_id=EXCLUDED.author_id, title=EXCLUDED.title, summary=EXCLUDED.summary, markdown=EXCLUDED.markdown`, ids[0], adminID, "Shared Responsibility Overview", "shared-responsibility-overview", "An independent study overview of shared responsibility.", "# Shared responsibility\n\nThis independently authored sample is not official CCAR-P material. Verify details against authoritative sources.")
		if err != nil {
			return err
		}
		var examID string
		err = tx.QueryRow(ctx, `INSERT INTO exams (author_id,title,slug,description,difficulty,time_limit_minutes,status,version,published_at) VALUES ($1,$2,$3,$4,'beginner',20,'published',1,now()) ON CONFLICT (slug) DO UPDATE SET author_id=EXCLUDED.author_id, title=EXCLUDED.title, description=EXCLUDED.description RETURNING id`, adminID, "Independent CCAR-P Practice Sample", "independent-ccar-p-practice-sample", "Four independently authored English questions; not official exam content.").Scan(&examID)
		if err != nil {
			return err
		}
		for i := 1; i <= 4; i++ {
			var questionID string
			err = tx.QueryRow(ctx, `INSERT INTO questions (exam_id,domain_id,prompt,explanation,difficulty,position) VALUES ($1,$2,$3,$4,'beginner',$5) ON CONFLICT (exam_id,position) DO UPDATE SET domain_id=EXCLUDED.domain_id,prompt=EXCLUDED.prompt,explanation=EXCLUDED.explanation RETURNING id`, examID, ids[i-1], fmt.Sprintf("Sample question %d: which choice best reflects the stated study principle?", i), "Review the associated domain and authoritative source for details.", i).Scan(&questionID)
			if err != nil {
				return err
			}
			for position, option := range []struct {
				key, text, explanation string
				correct                bool
			}{{"A", "Use the documented principle.", "This choice follows the documented study principle.", true}, {"B", "Ignore the documented principle.", "This choice conflicts with the documented study principle.", false}} {
				_, err = tx.Exec(ctx, `INSERT INTO question_options (question_id,option_key,text,explanation,is_correct,position) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (question_id,option_key) DO UPDATE SET text=EXCLUDED.text,explanation=EXCLUDED.explanation,is_correct=EXCLUDED.is_correct,position=EXCLUDED.position`, questionID, option.key, option.text, option.explanation, option.correct, position+1)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}
