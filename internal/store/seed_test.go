package store

import (
	"context"
	"strings"
	"testing"
)

func TestSeedRefusesNonDevelopment(t *testing.T) {
	s := &Store{}
	err := s.SeedDevelopment(context.Background(), "production", SeedAdmin{})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}
