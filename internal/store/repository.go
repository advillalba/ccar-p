package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Repositories struct {
	Domains *DomainRepository
}

type DomainRepository struct{ db DBTX }

type Domain struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Weight    int    `json:"weight"`
	SortOrder int    `json:"sort_order"`
}

func NewRepositories(db DBTX) Repositories { return Repositories{Domains: &DomainRepository{db: db}} }

func (r *DomainRepository) Active(ctx context.Context) ([]Domain, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, slug, weight, sort_order FROM domains WHERE is_active = $1 ORDER BY sort_order, id`, true)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Domain, 0)
	for rows.Next() {
		var item Domain
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.Weight, &item.SortOrder); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
