package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/ccar-p/study-platform/internal/config"
)

// TestRunMaintenanceValidatesBatches verifies that runMaintenance
// rejects invalid batch sizes up front.
func TestRunMaintenanceValidatesBatches(t *testing.T) {
	cfg := config.Config{}
	logger := slog.New(slog.NewJSONHandler(&discardWriter{}, nil))
	if _, err := runMaintenance(context.Background(), nil, cfg, logger, options{sessionBatch: 0, sessionMaxBatches: 1, output: "text"}); err == nil {
		t.Fatal("expected error for session-batch zero")
	}
	if _, err := runMaintenance(context.Background(), nil, cfg, logger, options{sessionBatch: 1, sessionMaxBatches: 0, output: "text"}); !errors.Is(err, errors.Unwrap(err)) && err == nil {
		t.Fatal("expected error for session-max-batches zero")
	}
}

// TestRunMaintenanceDryRunSkipsIO verifies that a dry-run invocation
// produces a Summary without touching state.
func TestRunMaintenanceDryRunSkipsIO(t *testing.T) {
	cfg := config.Config{}
	logger := slog.New(slog.NewJSONHandler(&discardWriter{}, nil))
	summary, err := runMaintenance(context.Background(), nil, cfg, logger, options{sessionBatch: 10, sessionMaxBatches: 1, dryRun: true, output: "text"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !summary.DryRun || summary.SessionsRemoved != 0 {
		t.Fatalf("dry run produced unexpected summary: %+v", summary)
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
