package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestOpenUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := Open(ctx, "postgres://postgres:postgres@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err == nil {
		t.Fatal("expected unavailable database error")
	}
}

func TestIntegrationTransactionRollbackAndTimeout(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	s, err := Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Pool.Exec(context.Background(), `CREATE TEMP TABLE tx_test (value text)`)
	if err != nil {
		t.Fatal(err)
	}
	expected := errors.New("rollback")
	err = s.WithinTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tx_test(value) VALUES ($1)`, "not committed")
		if err != nil {
			return err
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatalf("expected rollback error, got %v", err)
	}
	var count int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM tx_test`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback failed: count=%d err=%v", count, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Pool.Exec(ctx, `SELECT pg_sleep(1)`); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
}
