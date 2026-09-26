package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const DefaultQueryTimeout = 3 * time.Second

type Store struct {
	Pool         *pgxpool.Pool
	QueryTimeout time.Duration
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	cfg.MaxConns = 12
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 10 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unavailable: %w", err)
	}
	return &Store{Pool: pool, QueryTimeout: DefaultQueryTimeout}, nil
}

func (s *Store) Close() {
	s.Pool.Close()
}

// Exec forwards to the underlying pgxpool.Pool so the Store satisfies
// DBTX and can be wired into repositories that depend on it directly.
func (s *Store) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	ctx, cancel := s.withQueryTimeout(ctx)
	defer cancel()
	return s.Pool.Exec(ctx, sql, args...)
}

// Query forwards to the underlying pgxpool.Pool.
func (s *Store) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	ctx, cancel := s.withQueryTimeout(ctx)
	defer cancel()
	return s.Pool.Query(ctx, sql, args...)
}

// QueryRow forwards to the underlying pgxpool.Pool.
func (s *Store) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	ctx, cancel := s.withQueryTimeout(ctx)
	defer cancel()
	return s.Pool.QueryRow(ctx, sql, args...)
}

func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := s.withQueryTimeout(ctx)
	defer cancel()
	return s.Pool.Ping(ctx)
}

func (s *Store) WithinTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := s.withQueryTimeout(ctx)
	defer cancel()

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())

	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) withQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.QueryTimeout
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}
