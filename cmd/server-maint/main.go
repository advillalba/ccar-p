// Command server-maint performs bounded maintenance work for the
// ccar-p study platform: it removes expired or revoked authentication
// sessions from the database. The command is safe to run repeatedly;
// it only removes artifacts that are explicitly out of band.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ccar-p/study-platform/internal/config"
	"github.com/ccar-p/study-platform/internal/observability"
	"github.com/ccar-p/study-platform/internal/store"
)

func main() {
	sessionBatch := flag.Int("session-batch", 1000, "maximum number of expired sessions removed in a single batch")
	sessionsMaxBatches := flag.Int("session-max-batches", 10, "maximum number of batches attempted during a single invocation")
	dryRun := flag.Bool("dry-run", false, "log the maintenance actions that would be taken without modifying state")
	output := flag.String("output", "text", "output format: text or json")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	handler := observability.ChainLogger(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}), nil)
	logger := slog.New(handler)
	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	database, err := store.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("open database for maintenance", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	summary, err := runMaintenance(rootCtx, database, cfg, logger, options{
		sessionBatch:      *sessionBatch,
		sessionMaxBatches: *sessionsMaxBatches,
		dryRun:            *dryRun,
		output:            *output,
	})
	if err != nil {
		logger.Error("maintenance failed", "error", err)
		os.Exit(1)
	}
	switch *output {
	case "json":
		encoded, marshalErr := json.MarshalIndent(summary, "", "  ")
		if marshalErr != nil {
			fmt.Fprintln(os.Stderr, marshalErr)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, string(encoded))
	default:
		fmt.Fprintf(os.Stdout, "sessions removed: %d (batches: %d)\n", summary.SessionsRemoved, summary.SessionBatches)
	}
}

type options struct {
	sessionBatch      int
	sessionMaxBatches int
	dryRun            bool
	output            string
}

// Summary is the structured report produced by runMaintenance.
type Summary struct {
	SessionsRemoved int64     `json:"sessions_removed"`
	SessionBatches  int       `json:"session_batches"`
	DryRun          bool      `json:"dry_run"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
}

// runMaintenance executes the bounded maintenance steps. The function
// is exported via the options struct so tests can exercise each step
// independently. The database handle is expected to be open; closing
// is the caller's responsibility.
func runMaintenance(ctx context.Context, database *store.Store, cfg config.Config, logger *slog.Logger, opts options) (Summary, error) {
	if opts.sessionBatch <= 0 {
		return Summary{}, errors.New("session-batch must be greater than zero")
	}
	if opts.sessionMaxBatches <= 0 {
		return Summary{}, errors.New("session-max-batches must be greater than zero")
	}
	summary := Summary{StartedAt: time.Now().UTC(), DryRun: opts.dryRun}

	auth := store.NewAuthRepository(database)
	if !opts.dryRun {
		for batch := 0; batch < opts.sessionMaxBatches; batch++ {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			removed, err := auth.CleanupExpiredSessions(ctx, time.Now().UTC(), opts.sessionBatch)
			if err != nil {
				return summary, fmt.Errorf("cleanup expired sessions: %w", err)
			}
			summary.SessionBatches++
			summary.SessionsRemoved += removed
			logger.Info("session batch cleaned", "batch", batch+1, "removed", removed, "running_total", summary.SessionsRemoved)
			if removed < int64(opts.sessionBatch) {
				break
			}
		}
	} else {
		logger.Info("session cleanup skipped (dry-run)")
	}

	summary.CompletedAt = time.Now().UTC()
	return summary, nil
}
