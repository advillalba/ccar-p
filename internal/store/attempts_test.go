package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ccar-p/study-platform/internal/attempts"
)

func TestIntegrationUpsertAnswerAcceptsUUIDParameters(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	s, err := Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	repo := NewAttemptRepository(s.Pool)
	err = repo.UpsertAnswer(
		context.Background(),
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		[]string{"00000000-0000-0000-0000-000000000003"},
	)
	if !errors.Is(err, attempts.ErrNotInProgress) {
		t.Fatalf("expected ErrNotInProgress, got %v", err)
	}
}
