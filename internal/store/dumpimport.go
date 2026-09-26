package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// dumpColumn describes one column of a table in the public dump: its name and
// the PostgreSQL cast applied to the JSON value (always transported as text or
// NULL) so the importer does not depend on client-side type inference.
type dumpColumn struct {
	name string
	cast string
}

// dumpTable describes one exported table and its insert order (FK order).
type dumpTable struct {
	file    string
	table   string
	columns []dumpColumn
}

var dumpTables = []dumpTable{
	{
		file:  "users.jsonl",
		table: "users",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"email", ""},
			{"display_name", ""},
			{"password_hash", ""},
			{"role", "::user_role"},
			{"is_active", "::boolean"},
			{"last_login_at", "::timestamptz"},
			{"created_at", "::timestamptz"},
			{"updated_at", "::timestamptz"},
		},
	},
	{
		file:  "domains.jsonl",
		table: "domains",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"name", ""},
			{"slug", ""},
			{"description", ""},
			{"weight", "::smallint"},
			{"sort_order", "::smallint"},
			{"is_active", "::boolean"},
			{"created_at", "::timestamptz"},
			{"updated_at", "::timestamptz"},
		},
	},
	{
		file:  "exams.jsonl",
		table: "exams",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"author_id", "::uuid"},
			{"title", ""},
			{"slug", ""},
			{"description", ""},
			{"difficulty", "::difficulty_level"},
			{"time_limit_minutes", "::integer"},
			{"pass_percentage", "::numeric"},
			{"status", "::content_status"},
			{"version", "::integer"},
			{"published_at", "::timestamptz"},
			{"created_at", "::timestamptz"},
			{"updated_at", "::timestamptz"},
		},
	},
	{
		file:  "exam_versions.jsonl",
		table: "exam_versions",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"exam_id", "::uuid"},
			{"version", "::integer"},
			{"snapshot", "::jsonb"},
			{"created_at", "::timestamptz"},
		},
	},
	{
		file:  "questions.jsonl",
		table: "questions",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"exam_id", "::uuid"},
			{"domain_id", "::uuid"},
			{"prompt", ""},
			{"scenario", ""},
			{"explanation", ""},
			{"difficulty", "::difficulty_level"},
			{"position", "::integer"},
			{"created_at", "::timestamptz"},
			{"updated_at", "::timestamptz"},
		},
	},
	{
		file:  "question_options.jsonl",
		table: "question_options",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"question_id", "::uuid"},
			{"option_key", ""},
			{"text", ""},
			{"is_correct", "::boolean"},
			{"position", "::integer"},
			{"explanation", ""},
		},
	},
	{
		file:  "question_references.jsonl",
		table: "question_references",
		columns: []dumpColumn{
			{"id", "::uuid"},
			{"question_id", "::uuid"},
			{"title", ""},
			{"url", ""},
			{"citation", ""},
			{"position", "::integer"},
		},
	},
}

// DumpImportResult reports how many rows were inserted per table and whether
// the import was skipped because the database already contains data.
type DumpImportResult struct {
	Skipped bool
	Counts  map[string]int
}

// ImportPublicDumpIfEmpty loads a public JSONL dump (see
// db/export_public_dump.sh) inside a single transaction, but only when the
// database has no users yet (fresh deployment). Existing installations are
// never touched. If the dump directory does not exist the import is silently
// skipped so a checkout without the dump still boots.
func (s *Store) ImportPublicDumpIfEmpty(ctx context.Context, dir string) (DumpImportResult, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return DumpImportResult{Skipped: true, Counts: map[string]int{}}, nil
		}
	}
	result := DumpImportResult{Counts: map[string]int{}}
	err := s.withDumpTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var users int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
			return fmt.Errorf("check users: %w", err)
		}
		if users > 0 {
			result.Skipped = true
			return nil
		}
		counts, err := importDumpTables(ctx, tx, dir)
		if err != nil {
			return err
		}
		result.Counts = counts
		return nil
	})
	if err != nil {
		return DumpImportResult{}, err
	}
	return result, nil
}

// ImportPublicDump loads the dump unconditionally, inside one transaction.
// Intended for restores into an empty database.
func (s *Store) ImportPublicDump(ctx context.Context, dir string) (map[string]int, error) {
	var counts map[string]int
	err := s.withDumpTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		counts, err = importDumpTables(ctx, tx, dir)
		return err
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

func withDumpTx(ctx context.Context, s *Store, fn func(context.Context, pgx.Tx) error) error {
	dumpCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(dumpCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := fn(dumpCtx, tx); err != nil {
		return err
	}
	return tx.Commit(dumpCtx)
}

func (s *Store) withDumpTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return withDumpTx(ctx, s, fn)
}

func importDumpTables(ctx context.Context, tx pgx.Tx, dir string) (map[string]int, error) {
	counts := make(map[string]int, len(dumpTables))
	for _, t := range dumpTables {
		rows, err := readDumpFile(filepath.Join(dir, t.file), t.columns)
		if err != nil {
			if os.IsNotExist(err) {
				counts[t.table] = 0
				continue
			}
			return nil, err
		}
		inserted, err := insertDumpRows(ctx, tx, t, rows)
		if err != nil {
			return nil, err
		}
		counts[t.table] = inserted
	}
	return counts, nil
}

func insertDumpRows(ctx context.Context, tx pgx.Tx, t dumpTable, rows []map[string]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	names := make([]string, 0, len(t.columns))
	placeholders := make([]string, 0, len(t.columns))
	for i, c := range t.columns {
		names = append(names, c.name)
		placeholder := "$" + strconv.Itoa(i+1)
		if c.cast != "" {
			placeholder += c.cast
		}
		placeholders = append(placeholders, placeholder)
	}
	stmt := fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`,
		t.table,
		strings.Join(names, ", "),
		strings.Join(placeholders, ", "),
	)
	inserted := 0
	for _, row := range rows {
		args := make([]any, 0, len(t.columns))
		for _, c := range t.columns {
			args = append(args, dumpArg(row[c.name]))
		}
		tag, err := tx.Exec(ctx, stmt, args...)
		if err != nil {
			return inserted, fmt.Errorf("insert %s: %w", t.table, err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

func dumpArg(value any) any {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case map[string]any, []any:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(encoded)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// readDumpFile parses a JSONL file (one JSON object per line) and validates
// that every expected column key is present, so schema drift fails loudly
// instead of silently inserting NULLs.
func readDumpFile(path string, columns []dumpColumn) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var rows []map[string]any
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", filepath.Base(path), i+1, err)
		}
		for _, c := range columns {
			if _, ok := row[c.name]; !ok {
				return nil, fmt.Errorf("%s line %d: missing column %q", filepath.Base(path), i+1, c.name)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
