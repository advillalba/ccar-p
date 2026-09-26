package config

import (
	"strings"
	"testing"
)

func lookup(values map[string]string) Lookup {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestLoadDevelopment(t *testing.T) {
	cfg, err := LoadFrom(lookup(map[string]string{"DATABASE_URL": "postgres://localhost/ccarp"}))
	if err != nil || cfg.AppEnv != "development" || cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected config: %#v, %v", cfg, err)
	}
}

func TestProductionValidation(t *testing.T) {
	secret := "do-not-leak-this-production-secret"
	_, err := LoadFrom(lookup(map[string]string{
		"APP_ENV": "production", "DATABASE_URL": "postgres://localhost/db?sslmode=disable", "SESSION_SECRET": secret,
		"MCP_SERVICE_TOKEN_HASH": "sha256:redacted", "MCP_CONTENT_AUTHOR_ID": "00000000-0000-0000-0000-000000000001", "PUBLIC_BASE_URL": "https://study.example.test",
	}))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("expected safe production error, got %v", err)
	}
}

func TestMissingDatabase(t *testing.T) {
	_, err := LoadFrom(lookup(nil))
	if err == nil || err.Error() != "DATABASE_URL is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContentAuthorMustBeUUID(t *testing.T) {
	_, err := LoadFrom(lookup(map[string]string{
		"DATABASE_URL":          "postgres://localhost/ccarp",
		"MCP_CONTENT_AUTHOR_ID": "mcp:service-token",
	}))
	if err == nil || err.Error() != "MCP_CONTENT_AUTHOR_ID must be a UUID" {
		t.Fatalf("unexpected error: %v", err)
	}
}
