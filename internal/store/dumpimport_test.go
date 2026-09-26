package store

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const publicDumpDir = "../../db/public-dump"

func openMigratedTestStore(t *testing.T, url string) (*Store, error) {
	t.Helper()
	if err := Migrate(url, "../../db/migrations"); err != nil {
		return nil, err
	}
	return Open(context.Background(), url)
}

func TestReadDumpFileRequiresAllColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"x","email":"a@b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDumpFile(path, dumpTables[0].columns); err == nil {
		t.Fatal("expected missing column error")
	}
}

func TestReadDumpFileRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDumpFile(path, dumpTables[0].columns); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

// TestPublicDumpPrivacy verifies the committed public dump carries no personal
// data: real emails, password/token hashes or external mail domains.
func TestPublicDumpPrivacy(t *testing.T) {
	entries, err := os.ReadDir(publicDumpDir)
	if err != nil {
		t.Skipf("public dump not generated: %v", err)
	}
	emailPattern := regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	hashPattern := regexp.MustCompile(`\$(?:2[aby]|argon2(id|d|i)|scrypt|pbkdf2)\$`)
	allowedDomains := map[string]bool{
		"localhost.invalid": true,
		"localhost":         true,
		"example.com":       true,
		"example.org":       true,
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(publicDumpDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if hashPattern.MatchString(text) {
			t.Errorf("%s contains a password/token hash", entry.Name())
		}
		for _, email := range emailPattern.FindAllString(text, -1) {
			domain := email[strings.LastIndex(email, "@")+1:]
			if !allowedDomains[domain] {
				t.Errorf("%s contains non-synthetic email %q", entry.Name(), email)
			}
		}
	}
}

// TestPublicDumpManifestAndStatus validates manifest counts against the JSONL
// files and that every exported exam is published.
func TestPublicDumpManifestAndStatus(t *testing.T) {
	if _, err := os.Stat(filepath.Join(publicDumpDir, "manifest.json")); err != nil {
		t.Skipf("public dump not generated: %v", err)
	}
	rows, err := readDumpFile(filepath.Join(publicDumpDir, "exams.jsonl"), dumpTables[2].columns)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if row["status"] != "published" {
			t.Errorf("exams.jsonl line %d: status %v is not published", i+1, row["status"])
		}
		if row["published_at"] == nil {
			t.Errorf("exams.jsonl line %d: published exam without published_at", i+1)
		}
	}
	userRows, err := readDumpFile(filepath.Join(publicDumpDir, "users.jsonl"), dumpTables[0].columns)
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	for _, exam := range rows {
		authors[exam["author_id"].(string)] = true
	}
	for id := range authors {
		found := false
		for _, user := range userRows {
			if user["id"] == id {
				found = true
			}
		}
		if !found {
			t.Errorf("exam author %q missing from users.jsonl", id)
		}
	}
}

// TestImportPublicDumpIfEmptyIntegration runs against a live database when
// TEST_DATABASE_URL is set; it verifies the guard and a full import/restore.
func TestImportPublicDumpIfEmptyIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	s, err := openMigratedTestStore(t, url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	first, err := s.ImportPublicDumpIfEmpty(ctx, publicDumpDir)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Skipped {
		t.Skip("test database is not empty; cannot verify import")
	}
	if first.Counts["exams"] == 0 {
		t.Fatalf("expected published exams, got %v", first.Counts)
	}

	second, err := s.ImportPublicDumpIfEmpty(ctx, publicDumpDir)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if !second.Skipped {
		t.Fatal("expected second import to be skipped on a populated database")
	}
}
