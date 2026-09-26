// Command server is the entry point of the ccar-p study platform. It loads
// configuration, opens the PostgreSQL-backed store, wires every HTTP surface
// (public content, authentication, admin, attempts, remote MCP)
// plus health/readiness/metrics, and serves until SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ccar-p/study-platform/internal/attempts"
	"github.com/ccar-p/study-platform/internal/auth"
	"github.com/ccar-p/study-platform/internal/config"
	"github.com/ccar-p/study-platform/internal/httpapi"
	"github.com/ccar-p/study-platform/internal/mcp"
	"github.com/ccar-p/study-platform/internal/observability"
	"github.com/ccar-p/study-platform/internal/store"
)

const (
	// defaultRequestTimeout bounds every /api/v1 request via the API's
	// withTimeout middleware.
	defaultRequestTimeout = 10 * time.Second
	// defaultSessionLifetime matches the auth service's own 14-day fallback.
	defaultSessionLifetime = 14 * 24 * time.Hour
	// defaultShutdownGrace bounds connection draining after a signal.
	defaultShutdownGrace = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadWithDevelopmentDefaults()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger := slog.New(observability.ChainLogger(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}),
		nil,
	))

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	database, err := store.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Startup data preparation (see docs/public-dump.md): apply pending
	// migrations, then import the bundled public dump when the database is
	// still empty. Existing installations are never touched.
	if err := store.Migrate(cfg.DatabaseURL, "db/migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if result, err := database.ImportPublicDumpIfEmpty(rootCtx, "db/public-dump"); err != nil {
		return fmt.Errorf("import public dump: %w", err)
	} else if !result.Skipped {
		logger.Info("public dump imported", "counts", result.Counts)
	}

	repos := httpapi.NewStoreRepositories(database)
	publishingService := httpapi.NewStorePublishingService(repos)

	secureCookies := cfg.AppEnv == "production"
	authService := auth.NewService(store.NewAuthRepository(database), defaultSessionLifetime)
	contentRepository := store.NewContentRepository(database)
	examRepository := store.NewExamRepository(database)

	// API: mux plus its security/CORS/timeout middleware chain.
	api := httpapi.New(logger, cfg.CORSAllowedOrigins, defaultRequestTimeout)
	httpapi.NewAuthHandler(authService, cfg.SessionSecret, secureCookies).RegisterRoutes(api.Mux())
	httpapi.NewAdminHandlerWithPublishing(repos, authService, logger, nil, publishingService).RegisterRoutes(api.Mux())
	httpapi.NewNotesHandler(contentRepository).RegisterRoutes(api.Mux())
	httpapi.NewExamsHandlerWithPractice(
		examRepository,
		contentRepository,
		store.NewPracticeRepositoryWithStore(database),
		authService,
	).RegisterRoutes(api.Mux())
	httpapi.NewAttemptsHandler(
		attempts.NewService(store.NewAttemptRepositoryWithStore(database), examRepository),
		authService,
		secureCookies,
	).RegisterRoutes(api.Mux())
	// Remote MCP transport. In production a broken MCP configuration is
	// fatal; in development the server stays usable without the MCP route.
	mcpHandler, mcpErr := mcp.NewProductionHandler(mcp.ProductionOptions{
		Store:                    database,
		Publishing:               publishingService,
		BootstrapTokenHash:       cfg.MCPServiceTokenHash,
		BootstrapContentAuthorID: cfg.MCPContentAuthorID,
		BootstrapScopes:          mcp.AllScopes(),
		Logger:                   logger,
	})
	if mcpErr != nil && (cfg.AppEnv == "production" || !errors.Is(mcpErr, mcp.ErrTokenHashNotConfigured)) {
		// TokenHashNotConfigured only happens when the hash is missing/empty;
		// any other (parse) error also disables the route, so treat every
		// mount failure as fatal in production and tolerated in development.
		if cfg.AppEnv == "production" {
			return fmt.Errorf("mcp transport: %w", mcpErr)
		}
		logger.Warn("mcp transport disabled; MCP_SERVICE_TOKEN_HASH/MCP_CONTENT_AUTHOR_ID misconfigured", "error", mcpErr)
	}

	// Root mux: health, metrics, MCP, then the API. All wrapped with the
	// low-cardinality request metrics recorder.
	root := http.NewServeMux()
	health := observability.NewHealth(database, 2*time.Second)
	root.Handle("GET /healthz", http.HandlerFunc(health.LiveHandler))
	root.Handle("GET /readyz", http.HandlerFunc(health.ReadyHandler))
	root.Handle("GET /metrics", observability.NewMetrics().Handler())
	if mcpHandler != nil {
		root.Handle(mcp.Route, mcpHandler)
	}
	root.Handle("/", api.Handler)
	handler := observability.NewMetrics().RequestMiddleware(root)

	// WriteTimeout stays unbounded so MCP's long-lived SSE responses are
	// never cut; read phases and idle connections remain bounded.
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	logger.Info("server listening", "addr", cfg.HTTPAddr, "app_env", cfg.AppEnv, "secure_cookies", secureCookies)
	serveErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("listen: %w", err)
	case <-rootCtx.Done():
	}

	logger.Info("shutting down", "grace", defaultShutdownGrace)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	database.Close()
	logger.Info("server stopped")
	return nil
}
