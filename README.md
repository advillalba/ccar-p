# CCAR-P Study Platform

A Go, PostgreSQL, and Astro foundation for focused study notes, practice exams, and offline exports. Seed content is independently authored and is not official CCAR-P material.

**→ [Practice now — 822 questions, instant grading, no signup](https://advillalba.github.io/ccar-p/site/)**

**Live practice, no setup:** the public dump also powers a static practice site (822 questions, 4 difficulty levels, instant grading, progress kept in your browser). Every push to `main` deploys it via [`.github/workflows/gh-pages.yml`](.github/workflows/gh-pages.yml) — enable *Settings → Pages → Source: GitHub Actions* once. Serve it locally with:

```sh
cd web && PUBLIC_BASE_PATH=/ccar-p npx astro build --config astro.config.static.mjs
npx serve dist/client
```

## Stack

| Layer      | Technology                                        |
|------------|---------------------------------------------------|
| Backend    | Go 1.27, PostgreSQL 18, golang-migrate            |
| Frontend   | Astro 6 (SSR) + Preact islands, TypeScript        |
| Practices  | 822 independently authored questions, 12 domains  |
| MCP        | Anthropic MCP, bearer-token, audit trail          |
| Publishing | Content lifecycle: draft → published → archived   |

## Start

Copy `.env.example` to `.env`, replace every placeholder secret, then start the exactly three-service topology:

```sh
docker compose up --build -d
```

The frontend is available at `http://localhost:4321`. It is the only published application port. PostgreSQL and the Go backend remain on the private Compose network.

## Operations

```sh
docker compose logs -f frontend backend database
docker compose run --rm backend migrate
docker compose run --rm backend seed
docker compose run --rm backend server-maint
docker compose down
docker compose down --volumes
```

`seed` requires `APP_ENV=development` and `SEED_ADMIN_EMAIL`, `SEED_ADMIN_PASSWORD`, and `SEED_ADMIN_DISPLAY_NAME`. It is repeatable. `server-maint` removes expired sessions in bounded batches; it is also safe to run repeatedly. Recreate application containers without deleting database data using `docker compose up -d --force-recreate frontend backend`. Only `docker compose down --volumes` resets persistent data.

## Local verification

```sh
go test ./...
go vet ./...
npm --prefix web ci
npm --prefix web run check
npm --prefix web run build
docker compose config --services
docker compose build frontend backend
```

Set `TEST_DATABASE_URL` to run PostgreSQL integration tests. See `docs/local-development.md` for migration and lifecycle verification.

## Public content dump

`db/public-dump/*.jsonl` ships only published exam content plus a single sanitized author user. No notes, attempts, sessions, or personal data. On a clean `docker compose up` the backend auto-imports it when the database is empty (`docs/public-dump.md`). Regenerate with `db/export_public_dump.sh`.

## Documentation

| Document                              | Purpose                                        |
|---------------------------------------|------------------------------------------------|
| `docs/architecture.md`                | Service topology and backend layers.           |
| `docs/local-development.md`           | Compose workflow, tests, backups.              |
| `docs/deployment.md`                  | Production deployment, env vars, maintenance.  |
| `docs/security.md`                    | Secrets, log redaction, transport, audit.      |
| `docs/mcp.md`                         | MCP endpoint, auth, and available tools.       |
| `docs/content-authoring.md`           | Authoring notes and exams.                     |
| `docs/public-dump.md`                 | Public dump format, generation, and restore.   |

## License

[MIT](LICENSE) — study content in `db/public-dump/` is independently authored and not official CCAR-P material.