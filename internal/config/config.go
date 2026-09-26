package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	AppEnv              string
	HTTPAddr            string
	DatabaseURL         string
	SessionSecret       string
	MCPServiceTokenHash string
	MCPContentAuthorID  string
	PublicBaseURL       string
	LogLevel            slog.Level
	CORSAllowedOrigins  []string
}

type Lookup func(string) (string, bool)

func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

func LoadFrom(lookup Lookup) (Config, error) {
	cfg := Config{
		AppEnv:              value(lookup, "APP_ENV", "development"),
		HTTPAddr:            value(lookup, "HTTP_ADDR", ":8080"),
		DatabaseURL:         value(lookup, "DATABASE_URL", ""),
		SessionSecret:       value(lookup, "SESSION_SECRET", ""),
		MCPServiceTokenHash: value(lookup, "MCP_SERVICE_TOKEN_HASH", ""),
		MCPContentAuthorID:  value(lookup, "MCP_CONTENT_AUTHOR_ID", ""),
		PublicBaseURL:       value(lookup, "PUBLIC_BASE_URL", "http://localhost:4321"),
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(value(lookup, "LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error: %w", err)
	}
	for _, origin := range strings.Split(value(lookup, "CORS_ALLOWED_ORIGINS", cfg.PublicBaseURL), ",") {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			cfg.CORSAllowedOrigins = append(cfg.CORSAllowedOrigins, trimmed)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.AppEnv != "development" && c.AppEnv != "test" && c.AppEnv != "production" {
		return errors.New("APP_ENV must be development, test, or production")
	}
	if c.HTTPAddr == "" {
		return errors.New("HTTP_ADDR is required")
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	base, err := url.ParseRequestURI(c.PublicBaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return errors.New("PUBLIC_BASE_URL must be an absolute HTTP(S) URL")
	}
	if c.MCPContentAuthorID != "" && !isUUID(c.MCPContentAuthorID) {
		return errors.New("MCP_CONTENT_AUTHOR_ID must be a UUID")
	}
	if c.AppEnv == "production" {
		if len(c.SessionSecret) < 32 {
			return errors.New("SESSION_SECRET must contain at least 32 characters in production")
		}
		if c.MCPServiceTokenHash == "" {
			return errors.New("MCP_SERVICE_TOKEN_HASH is required in production")
		}
		if c.MCPContentAuthorID == "" {
			return errors.New("MCP_CONTENT_AUTHOR_ID is required in production")
		}
		if base.Scheme != "https" {
			return errors.New("PUBLIC_BASE_URL must use HTTPS in production")
		}
		if !strings.Contains(strings.ToLower(c.DatabaseURL), "sslmode=require") && !strings.Contains(strings.ToLower(c.DatabaseURL), "sslmode=verify-ca") && !strings.Contains(strings.ToLower(c.DatabaseURL), "sslmode=verify-full") {
			return errors.New("DATABASE_URL must require TLS in production")
		}
	}
	for _, origin := range c.CORSAllowedOrigins {
		u, err := url.ParseRequestURI(origin)
		if err != nil || u.Host == "" || u.Path != "" {
			return errors.New("CORS_ALLOWED_ORIGINS must contain absolute origins")
		}
	}
	return nil
}

func value(lookup Lookup, key, fallback string) string {
	if v, ok := lookup(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(compact)
	return err == nil
}

// LoadWithDevelopmentDefaults wraps Load and, in non-production
// environments, substitutes the shipped "replace-with-*" placeholder
// values for MCP_SERVICE_TOKEN_HASH and MCP_CONTENT_AUTHOR_ID with
// syntactically valid defaults. Production keeps the stricter
// validation so misconfigured secrets fail loudly. The zeroed hash
// merely disables bootstrap-token authentication, and the all-zeroes
// UUID stands in for the configured content author until real values
// are supplied.
func LoadWithDevelopmentDefaults() (Config, error) {
	if os.Getenv("APP_ENV") == "production" {
		return Load()
	}
	if hash := strings.TrimPrefix(os.Getenv("MCP_SERVICE_TOKEN_HASH"), "sha256:"); hash == "" || strings.HasPrefix(hash, "replace-with") {
		os.Setenv("MCP_SERVICE_TOKEN_HASH", "sha256:"+strings.Repeat("0", 64))
	}
	if author := os.Getenv("MCP_CONTENT_AUTHOR_ID"); author == "" || !isUUID(author) {
		os.Setenv("MCP_CONTENT_AUTHOR_ID", "00000000-0000-0000-0000-000000000000")
	}
	return Load()
}

func isPlaceholder(value string) bool {
	return strings.HasPrefix(value, "replace-with") || !isUUID(value)
}
