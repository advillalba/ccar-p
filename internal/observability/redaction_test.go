package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

func TestRedactingHandlerStripsSensitiveKeysAndValues(t *testing.T) {
	buffer := &bytes.Buffer{}
	inner := slog.NewJSONHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewRedactingHandler(inner, nil)
	logger := slog.New(handler)
	logger.Info("publishing note",
		"user_id", "user-1",
		"session_token", "secret-session-token",
		"csrf_hash", "csrf-abc",
		"authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.signature",
		"request_id", "req-1",
	)
	raw := buffer.String()
	for _, forbidden := range []string{
		"secret-session-token",
		"csrf-abc",
		"eyJhbGciOiJIUzI1NiJ9",
		"signature",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("log contained %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(raw, "[REDACTED]") {
		t.Fatalf("expected redaction marker in log: %s", raw)
	}
	if !strings.Contains(raw, `"request_id":"req-1"`) {
		t.Fatalf("expected non-sensitive attribute to survive: %s", raw)
	}
}

func TestRedactingHandlerStripsPostgresURL(t *testing.T) {
	buffer := &bytes.Buffer{}
	handler := NewRedactingHandler(slog.NewJSONHandler(buffer, nil), nil)
	logger := slog.New(handler)
	logger.Info("database url leaked", "database_url", "postgres://user:p%40ssword@db.local:5432/ccarp?sslmode=require")
	if strings.Contains(buffer.String(), "p%40ssword") {
		t.Fatalf("log leaked database password: %s", buffer.String())
	}
}

func TestRedactingHandlerWithGroupPreservesRedaction(t *testing.T) {
	buffer := &bytes.Buffer{}
	handler := NewRedactingHandler(slog.NewJSONHandler(buffer, nil), nil).WithGroup("audit")
	logger := slog.New(handler)
	logger.Info("publication recorded", "actor", "admin", "session", "should-be-redacted")
	if strings.Contains(buffer.String(), "should-be-redacted") {
		t.Fatalf("log leaked session attribute: %s", buffer.String())
	}
}

func TestRedactingHandlerExtraPatternHonored(t *testing.T) {
	buffer := &bytes.Buffer{}
	handler := NewRedactingHandler(slog.NewJSONHandler(buffer, nil), &RedactingOptions{
		ExtraPatterns: []*regexp.Regexp{regexp.MustCompile(`tk_[A-Za-z0-9]+`)},
	})
	slog.New(handler).Info("custom token", "value", "tk_abcdef123")
	if strings.Contains(buffer.String(), "tk_abcdef123") {
		t.Fatalf("extra pattern did not redact: %s", buffer.String())
	}
}

func TestRedactingHandlerDisabledMatchesInner(t *testing.T) {
	handler := NewRedactingHandler(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}), nil)
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected redaction handler to defer Enabled to inner handler")
	}
}

func TestRedactingHandlerPropagatesError(t *testing.T) {
	inner := slog.NewJSONHandler(&bytes.Buffer{}, nil)
	handler := NewRedactingHandler(inner, &RedactingOptions{Inner: inner})
	if err := handler.Handle(context.Background(), slog.Record{Level: slog.LevelInfo, Message: "ok"}); err != nil {
		t.Fatalf("unexpected handle error: %v", err)
	}
}

func TestRedactingHandlerEmitsJSON(t *testing.T) {
	buffer := &bytes.Buffer{}
	handler := NewRedactingHandler(slog.NewJSONHandler(buffer, nil), nil)
	slog.New(handler).Info("json", "safe", "value")
	var decoded map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatalf("output was not valid JSON: %v\n%s", err, buffer.String())
	}
}
