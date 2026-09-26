#!/usr/bin/env bash
#
# Export the publishable subset of the database as JSONL files (one JSON object
# per line) into db/public-dump/. The dump contains only published exam content
# plus a single sanitized service author row (the user referenced by
# MCP_CONTENT_AUTHOR_ID). It never contains real users, notes, attempts,
# sessions, tokens or audit data.
#
# Usage:
#   db/export_public_dump.sh [output-dir]
#
# Environment:
#   DUMP_CONTAINER  Docker container running PostgreSQL (default: ccarp-database)

set -euo pipefail

out_dir="${1:-$(cd "$(dirname "$0")" && pwd)/public-dump}"
container="${DUMP_CONTAINER:-ccarp-database}"

query() {
  docker exec -e DUMP_SQL="$1" "$container" sh -c \
    'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -Atc "$DUMP_SQL"'
}

mkdir -p "$out_dir"

# users: only the service author(s) of published exams, fully sanitized.
# The email and password hash are replaced with inert sentinels.
query "SELECT row_to_json(x) FROM (
  SELECT u.id,
         'mcp-author@localhost.invalid'::text AS email,
         u.display_name,
         '!'::text AS password_hash,
         u.role,
         u.is_active,
         NULL::timestamptz AS last_login_at,
         u.created_at,
         u.updated_at
  FROM users u
  WHERE u.id IN (SELECT DISTINCT author_id FROM exams WHERE status = 'published')
) x" > "$out_dir/users.jsonl"

query "SELECT row_to_json(d) FROM domains d" > "$out_dir/domains.jsonl"

query "SELECT row_to_json(e) FROM exams e WHERE e.status = 'published'" > "$out_dir/exams.jsonl"

query "SELECT row_to_json(v) FROM exam_versions v
  JOIN exams e ON e.id = v.exam_id
  WHERE e.status = 'published' AND v.version = e.version" > "$out_dir/exam_versions.jsonl"

query "SELECT row_to_json(q) FROM questions q
  JOIN exams e ON e.id = q.exam_id
  WHERE e.status = 'published'" > "$out_dir/questions.jsonl"

query "SELECT row_to_json(o) FROM question_options o
  JOIN questions q ON q.id = o.question_id
  JOIN exams e ON e.id = q.exam_id
  WHERE e.status = 'published'" > "$out_dir/question_options.jsonl"

query "SELECT row_to_json(r) FROM question_references r
  JOIN questions q ON q.id = r.question_id
  JOIN exams e ON e.id = q.exam_id
  WHERE e.status = 'published'" > "$out_dir/question_references.jsonl"

# manifest: generation date, schema version and row counts for traceability.
manifest="$out_dir/manifest.json"
schema_version=$(query "SELECT version FROM schema_migrations")
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
tmp_manifest="$manifest"
cat > "$tmp_manifest" <<EOF
{
  "generated_at": "$created_at",
  "schema_version": $schema_version,
EOF
first=1
for f in users domains exams exam_versions questions question_options question_references; do
  count=$(wc -l < "$out_dir/$f.jsonl" | tr -d ' ')
  [ $first -eq 0 ] && echo ',' >> "$tmp_manifest"
  first=0
  printf '  "%s": %s' "$f" "$count" >> "$tmp_manifest"
done
echo '' >> "$tmp_manifest"
echo '}' >> "$tmp_manifest"

echo "Public dump written to $out_dir:"
ls -l "$out_dir"
