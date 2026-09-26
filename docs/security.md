# Security

This document describes the security posture of the ccar-p study
platform, the controls enforced by the application, and the
responsibilities of operators running the platform.

## Secrets and credentials

| Secret                       | Source                          | Rotation                |
|------------------------------|---------------------------------|-------------------------|
| `SESSION_SECRET`             | Operator-supplied random value  | Quarterly or on incident|
| `MCP_SERVICE_TOKEN_HASH`     | Hashed bearer token             | On personnel change     |
| `POSTGRES_PASSWORD`          | Compose secret                  | Quarterly                |
| `DATABASE_URL`               | Compose secret                  | Quarterly                |

`SESSION_SECRET` must be at least 32 random characters. `config.Load`
rejects values shorter than this in production.

`MCP_SERVICE_TOKEN_HASH` is a one-way hash of the bearer token used
by external MCP clients. The cleartext token is never stored by the
application; only the hash is persisted.

## Log redaction

The structured logger is wrapped by
`observability.NewRedactingHandler`, which removes secrets from every
log record before it is written. The wrapper applies two layers:

1. **Key-based redaction.** Any attribute whose key contains
   `password`, `secret`, `token`, `session`, `csrf`, `cookie`,
   `authorization`, `hash`, `credential`, `apikey`, or `private` is
   replaced with `[REDACTED]`.
2. **Value-based redaction.** Regex patterns catch PostgreSQL DSNs,
   JWTs, bearer tokens, and `SESSION_SECRET=...` query parameters
   even if they appear in unrelated attributes or messages.

The redaction layer is exercised by
`internal/observability/redaction_test.go`, which captures logs and
asserts that secret-bearing fixtures never reach the underlying
handler. Operators must rely on this layer instead of hand-filtering
secrets out of log messages.

## Transport security

- The PostgreSQL DSN must enable TLS in production.
- The public origin must use HTTPS in production.
- Cookies are issued with the `Secure`, `HttpOnly`, and `SameSite=Lax`
  attributes by `internal/httpapi`.

## Authentication

- Passwords are hashed with Argon2id; the parameters live in
  `internal/auth/password.go`.
- Sessions are stored in the database; tokens are hashed before
  storage, so a database leak does not yield valid session tokens.
- CSRF tokens are issued alongside each session and required for any
  state-changing request.

## Authorization

- Roles are `student` and `admin`.
- Admin-only routes are guarded by `internal/httpapi`'s authentication
  middleware.

## Input validation

- Markdown content is rendered through `internal/content` validation
  and sanitization before storage and re-rendered for HTML output.
- HTTP handlers reject oversized bodies via `api.limitBody`.
- All write requests are bounded by a context timeout via
  `api.withTimeout`.

## Rate limiting and throttling

- A middleware enforces a per-IP request cap; metrics for throttled
  requests are exported under the `http_throttled_total` counter with
  bounded labels (`route`, `status_class`).

## Audit logging

Every publication event writes a row into `publication_events`,
capturing the actor, outcome, and reason. Operators can query the
table to audit who published what and when.

## Operator responsibilities

- Rotate `SESSION_SECRET`, `MCP_SERVICE_TOKEN_HASH`, and the database
  password on the cadence above.
- Restrict access to the backend and database containers to operators
  on the trusted network. Only the frontend is exposed externally.
- Keep Docker base images patched. The base image is rebuilt on every
  `docker compose build`.
- Back up the database daily and rehearse restores quarterly.
- Run `server-maint` hourly to keep the sessions table bounded.

## Reporting vulnerabilities

Please report security issues privately by emailing the maintainer.
Do not file public GitHub issues for suspected vulnerabilities.