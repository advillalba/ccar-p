# Public dump

Distribución pública del contenido examinable sin datos personales.

## Qué contiene `db/public-dump/`

Archivos JSONL (una fila JSON por línea) + `manifest.json`:

| Archivo | Contenido |
|---|---|
| `users.jsonl` | Único usuario autor del contenido (MCP). Sanitizado: `email=mcp-author@localhost.invalid`, `password_hash="!"`, `last_login_at=null`. |
| `domains.jsonl` | Dominios temáticos. |
| `exams.jsonl` | Exámenes con `status=published` únicamente. |
| `exam_versions.jsonl` | Versión vigente de cada examen publicado (`exam_versions.version = exams.version`). Historial descartado. |
| `questions.jsonl` | Preguntas de exámenes publicados. |
| `question_options.jsonl` | Opciones de esas preguntas. |
| `question_references.jsonl` | Referencias (vacío si no hay). |
| `manifest.json` | `generated_at`, `schema_version` y conteos por tabla. |

Quedan **excluidos** a propósito: `notes`, `note_tags`, `tags`, `note_references`, `sessions`, `service_tokens`, `attempts`, `attempt_answers`, `practice_answers`, `publication_events`, `mcp_audit_events`, y cualquier otro usuario.

## Generar el dump

```sh
# Contra la BD de producción (lee vía docker exec, no necesita DATABASE_URL)
db/export_public_dump.sh [db/public-dump]

# Variable opcional para otro contenedor
DUMP_CONTAINER=ccarp-database db/export_public_dump.sh
```

El script consulta solo filas `published` vía `row_to_json` y sanitiza el usuario. Requiere `docker exec` al contenedor Postgres.

## Restaurar / arranque automático

*Arranque sin intervención:* `cmd/server/main.go:37` ejecuta en cada inicio:

1. `store.Migrate` (`db/migrations`).
2. `store.ImportPublicDumpIfEmpty` (`internal/store/dumpimport.go:145`) — comprueba `SELECT count(*) FROM users`; si es 0, importa el dump en una transacción con `ON CONFLICT DO NOTHING`. Si la BD ya tiene datos o el directorio no existe, no hace nada.

Por tanto `docker compose up --build -d` en un clon limpio (volumen nuevo) carga el contenido automáticamente.

*Restauración manual:*

```sh
DATABASE_URL=postgres://... go run ./cmd/importdump [db/public-dump]
# o dentro del contenedor
docker compose run --rm backend importdump
```

La importación es idempotente y respeta el orden FK: `users → domains → exams → exam_versions → questions → question_options → question_references`.

## Privacidad

* El dump nunca incluye `service_tokens.token_hash` (solo hash, nunca el bearer en claro que vive en `secrets/`).
* Emails sintéticos `@localhost.invalid`; hashes reemplazados.
* Tests de privacidad: `internal/store/dumpimport_test.go:TestPublicDumpPrivacy` — falla si aparece un email real o un hash `$2a$`/`$argon2`.
* Antes de publicar, ejecutar `db/export_public_dump.sh` y `go test ./...`.

## Imagen Docker

`Dockerfile.backend:15` copia `db/public-dump` a `/app/db/public-dump` para que la auto-carga funcione sin volumen externo.
