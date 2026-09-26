# CCAR-P Agent Guide

## Project

- Go backend and MCP server: `cmd/`, `internal/`, `db/`.
- Astro frontend: `web/`.
- Backend entrypoint: `cmd/server`.
- Database migrations: `db/migrations`; runner: `cmd/migrate`.
- MCP production wiring: `internal/mcp/wiring.go`.
- MCP tools: `internal/mcp/tools_*.go`.
- PostgreSQL repositories: `internal/store/`.

## Deployment

- Existing deployment directory: `/home/alvaro/server/ai-helpers`.
- Compose file: `/home/alvaro/server/ai-helpers/ccar-p.compose.yml`.
- Always pass `/home/alvaro/server/ai-helpers/environments/ccar-p.properties` with `--env-file` so `FRONTEND_PORT` is defined.
- Secrets are in `/home/alvaro/server/ai-helpers/secrets/ccar-p.properties`; never print, copy into source, or log them.
- Bootstrap bearer is in `/home/alvaro/server/ai-helpers/secrets/ccar-p-mcp-bootstrap.token`; never expose it.
- Public endpoint: `https://ccar-p.buildspace.run`; MCP route: `/mcp`.
- Containers: `ccarp-database`, `ccarp-backend`, `ccarp-frontend`.
- Preserve database and secrets volumes. Never run `down -v`, remove volumes, or recreate the database unless explicitly approved.
- Deploy only changed application services:

```bash
docker compose --env-file environments/ccar-p.properties -f ccar-p.compose.yml build ccarp-backend ccarp-frontend
docker compose --env-file environments/ccar-p.properties -f ccar-p.compose.yml up -d --no-deps ccarp-backend ccarp-frontend
```

- Run migrations from the newly built backend image without dependencies:

```bash
docker compose --env-file environments/ccar-p.properties -f ccar-p.compose.yml run --rm --no-deps ccarp-backend migrate
```

## Database Constraints

- Keep foreign keys enabled and surface PostgreSQL errors accurately.
- `exams.author_id` and other content author fields require an active `users.id` UUID.
- MCP token identity and content author identity are distinct: `Actor.ID` identifies the service token; `ContentAuthorID` identifies the user author.
- `MCP_CONTENT_AUTHOR_ID` must reference an active user.
- `publication_events.actor_id` is text as of migration 7 because service actor identities are not necessarily UUIDs.

## MCP

- New tools must be registered in `internal/mcp/wiring.go` and use explicit scopes from `internal/mcp/registry.go`.
- Mutation tool names beginning with `create_`, `update_`, or `delete_` are audited automatically.
- Add target classification in `auditTarget` when introducing a new resource type.
- Use `decodeStrict` and structured `ToolError` responses.
- Domain MCP tools are `list_domains`, `get_domain`, `create_domain`, `update_domain`, and `delete_domain` with `domains:read` and `domains:write` scopes.
- Domain deletion is hard deletion and PostgreSQL rejects it while notes or questions reference the domain; deactivation through `update_domain` is preferred for used domains.

## Verification

Run after Go changes:

```bash
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go vet ./...
test -z "$(gofmt -l cmd internal)"
```

Run after frontend changes:

```bash
npm --prefix web run check
```

After deployment verify:

```bash
docker compose --env-file environments/ccar-p.properties -f ccar-p.compose.yml ps
curl --fail --silent --show-error https://ccar-p.buildspace.run/health
```

Inspect backend logs with `docker logs --since 10m ccarp-backend`. An unauthenticated POST to `/mcp` should return HTTP 401.
