# Deployment

This document describes how to deploy the ccar-p study platform to a
production-like environment. The platform runs as a three-service
topology: an Astro frontend, a Go backend, and a PostgreSQL database.

## Topology

| Service  | Image source                        | Published port | Network          |
|----------|--------------------------------------|----------------|------------------|
| frontend | `web/` Astro build served by Node    | `4321`         | `ccarp-internal` |
| backend  | `Dockerfile.backend` Go binary       | not exposed    | `ccarp-internal` |
| database | `postgres:16`                        | not exposed    | `ccarp-internal` |

Only the frontend is reachable from outside the Compose network. The
backend listens on its private network so the Astro SSR layer can call
it without leaving the trusted boundary.

## Environment variables

| Variable                  | Required in production | Notes                                              |
|---------------------------|------------------------|----------------------------------------------------|
| `APP_ENV`                 | yes                    | Must be `production`.                               |
| `HTTP_ADDR`               | yes                    | The address the backend binds to.                  |
| `DATABASE_URL`            | yes                    | TLS-enabled PostgreSQL DSN.                        |
| `SESSION_SECRET`          | yes                    | At least 32 random characters; never logged.       |
| `MCP_SERVICE_TOKEN_HASH`  | yes                    | Hashed bearer token for MCP clients.               |
| `PUBLIC_BASE_URL`         | yes                    | Must be an `https://` origin.                      |
| `LOG_LEVEL`               | optional               | One of `debug`, `info`, `warn`, `error`.           |
| `CORS_ALLOWED_ORIGINS`    | yes                    | Comma-separated list of allowed origins.           |

`config.Load` rejects any configuration that does not satisfy the
production rules above.

## Image build

```sh
docker compose build frontend backend
```

`Dockerfile.backend` produces three binaries (`server`, `migrate`,
`seed`, `server-maint`). `migrate` and `seed` run as one-shot commands
during the bring-up phase; `server-maint` runs as a periodic job for
session cleanup.

## Bring-up

```sh
docker compose run --rm backend migrate
docker compose run --rm backend seed
docker compose up -d backend frontend
```

The `seed` command is repeatable. It inserts an initial admin account
and domain taxonomy only when the table is empty.

## Maintenance

```sh
docker compose run --rm backend server-maint
```

The `server-maint` command performs one bounded job:

1. Remove expired or revoked authentication sessions in batches of
   `--session-batch` rows, up to `--session-max-batches` times per
   invocation. Repeated runs are safe because each batch is bounded.

The command supports `--dry-run` and `--output json` for automation.
A typical cron entry is `server-maint --session-batch 1000
--session-max-batches 10 --output json` running hourly.

## Backup and restore

See `docs/local-development.md` for the recommended PostgreSQL
backup and restore flow. Production deployments should run a daily
`pg_dump` into cold storage and rehearse restores into a staging
environment at least once per quarter.

## Health checks

The backend exposes:

- `GET /healthz` — liveness; always returns `200`.
- `GET /readyz` — readiness; checks the PostgreSQL connection.

Reverse proxies should consider `/readyz` failing as the signal to
remove the backend from rotation while keeping the process running.
This makes a database outage visible without restarting the binary.

## Observability

Logs are emitted as JSON to stdout with the following guarantees:

- Every record includes a `request_id` attribute when the request
  context carries one.
- Sensitive attributes (anything whose key contains `password`,
  `secret`, `token`, `session`, `csrf`, `cookie`, `authorization`,
  `hash`, `credential`, or `apikey`) are redacted to `[REDACTED]`.
- Sensitive values (PostgreSQL DSNs, JWTs, bearer tokens, session
  cookies) are redacted using regex patterns.

Metrics are exposed in Prometheus exposition format on the private
backend port. Scrape the backend from the private network; do not
expose metrics on the public network.