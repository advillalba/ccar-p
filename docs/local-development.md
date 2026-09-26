# Local Development

## Configuration

The backend reads `APP_ENV`, `HTTP_ADDR`, `DATABASE_URL`, `SESSION_SECRET`, `MCP_SERVICE_TOKEN_HASH`, `PUBLIC_BASE_URL`, `LOG_LEVEL`, and `CORS_ALLOWED_ORIGINS`. Development seeds additionally read `SEED_ADMIN_EMAIL`, `SEED_ADMIN_PASSWORD`, and `SEED_ADMIN_DISPLAY_NAME`. Compose reads `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, and optional `FRONTEND_PORT`.

Secrets belong in an untracked `.env` or the deployment secret manager. Production requires HTTPS, database TLS, a session secret of at least 32 characters, and an MCP bootstrap token hash.

## Compose workflow

1. Copy `.env.example` to `.env` and replace placeholders.
2. Run `docker compose up --build -d`.
3. Run `docker compose run --rm backend migrate`.
4. Optionally run `docker compose run --rm backend seed` in development.
5. Optionally run `docker compose run --rm backend server-maint` to
   exercise the maintenance command.
6. Inspect with `docker compose ps` and `docker compose logs -f frontend backend database`.
7. Stop with `docker compose down`.

The health dependency order is database, backend, frontend. Verify persistence by seeding, running `docker compose up -d --force-recreate frontend backend`, and confirming the seeded rows remain. `docker compose down --volumes` is the explicit destructive reset.

## Database tests

Start the database, obtain its internal URL from a test process on the Compose network, and set `TEST_DATABASE_URL`. Integration tests apply migrations to an isolated database and verify rollback, query cancellation, constraints, cleanup, and repeatable seeds. Never point integration tests at shared or production data.

## Local Go verification

```sh
PATH=/tmp/opencode/go/bin:$PATH go build ./...
PATH=/tmp/opencode/go/bin:$PATH go vet ./...
PATH=/tmp/opencode/go/bin:$PATH go test ./...
```

The platform requires Go 1.24. Use the local toolchain at
`/tmp/opencode/go/bin` when the system Go is older.

## Frontend verification

```sh
npm --prefix web ci
npm --prefix web run check
npm --prefix web run build
```

## Database backup and restore

```sh
# Backup from the running Compose database:
docker compose exec -T database pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" > backup.sql

# Restore into a fresh database (drops and recreates the target database):
dropdb -U "$POSTGRES_USER" --if-exists "$POSTGRES_DB"
createdb -U "$POSTGRES_USER" "$POSTGRES_DB"
psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f backup.sql
```

In Compose, the equivalent uses the running container:

```sh
docker compose exec -T database pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" > backup.sql
cat backup.sql | docker compose exec -T database psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"
```

Schedule backups as part of the operations runbook described in
`docs/deployment.md`. Restore into a staging environment at least once
per quarter to confirm the backup is usable.

## Maintenance verification

The maintenance command can be exercised locally:

```sh
docker compose run --rm backend server-maint --dry-run --output json
docker compose run --rm backend server-maint --session-batch 100 --session-max-batches 5
```

The first command logs the actions without modifying state. The
second command removes up to 500 expired sessions in five batches.
Repeated runs are safe and idempotent.