# Foundation Architecture

The baseline has exactly three runtime services. Astro is the public entry point and proxies `/api` to the private Go backend. Go owns REST, health, migrations, seeds, MCP, and a periodic maintenance command. PostgreSQL is the source of truth and uses a named volume.

## Layers

The backend separates configuration, HTTP transport, authentication, content, exams, attempts, publishing, MCP, persistence, and observability under `internal/`. The package boundaries mirror the bounded contexts above so dependencies flow downward.

| Package               | Responsibility                                          |
|-----------------------|---------------------------------------------------------|
| `internal/config`     | Environment-driven configuration with validation.       |
| `internal/auth`       | Argon2id password hashing, session/CSRF helpers.         |
| `internal/content`    | Markdown parsing, validation, and HTML rendering.        |
| `internal/exams`      | Exam domain model and snapshot logic.                    |
| `internal/attempts`   | Exam attempt lifecycle and scoring.                      |
| `internal/publishing` | Publication pipeline and audit logging.                  |
| `internal/mcp`        | Model Context Protocol adapter.                          |
| `internal/store`      | SQL repositories backed by pgxpool.                     |
| `internal/httpapi`    | HTTP transport, middleware, and request envelopes.       |
| `internal/observability` | Logger redaction, correlation, metrics, health.       |

## Persistence

SQL repositories accept a narrow `DBTX` interface, use PostgreSQL placeholders, and receive request-derived bounded contexts. Multi-record writes use `Store.WithinTx`. Migrations are ordered and forward-applied. The initial migration defines the complete baseline model. The integrity migration adds timestamp and deferred cross-row protections, bounded session cleanup, and search indexes. Published exam versions and attempt JSON snapshots preserve historical review data.

## Observability

The backend exposes:

- `/healthz` — process liveness.
- `/readyz` — readiness probe that checks PostgreSQL connectivity.
- `/metrics` — Prometheus exposition with bounded labels
  (`route`, `status_class`, `entity`, `tool`).
- Structured JSON logs to stdout with `request_id` correlation and
  secret redaction.

See `docs/security.md` for the redaction contract.

## Maintenance

The `server-maint` command is a bounded cleanup job that runs
periodically. It removes expired and revoked authentication sessions
in bounded batches. The cleanup is idempotent; see
`docs/deployment.md` for the recommended schedule.