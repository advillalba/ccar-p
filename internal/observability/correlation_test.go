package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestCorrelationHandlerAttachesRequestID(t *testing.T) {
	buffer := &bytes.Buffer{}
	handler := NewCorrelationHandler(slog.NewJSONHandler(buffer, nil))
	logger := slog.New(handler)
	logger.InfoContext(WithCorrelation(context.Background(), "req-abc"), "operation finished")
	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("output was not JSON: %v", err)
	}
	if record["request_id"] != "req-abc" {
		t.Fatalf("expected request_id attribute, got %v", record["request_id"])
	}
}

func TestCorrelationHandlerEmptyIDOmitsAttribute(t *testing.T) {
	buffer := &bytes.Buffer{}
	handler := NewCorrelationHandler(slog.NewJSONHandler(buffer, nil))
	slog.New(handler).InfoContext(context.Background(), "no correlation")
	if strings.Contains(buffer.String(), "request_id") {
		t.Fatalf("did not expect request_id attribute: %s", buffer.String())
	}
}

func TestChainLoggerRedactsAndAttachesRequestID(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := slog.New(ChainLogger(slog.NewJSONHandler(buffer, nil), nil))
	logger.InfoContext(WithCorrelation(context.Background(), "req-1"), "publication recorded", "session", "leaked")
	output := buffer.String()
	if strings.Contains(output, "leaked") {
		t.Fatalf("session attribute leaked: %s", output)
	}
	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("output was not JSON: %v", err)
	}
	if record["request_id"] != "req-1" {
		t.Fatalf("missing request_id attribute: %v", record)
	}
}

func TestFromContextReturnsBaseWhenMissing(t *testing.T) {
	base := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	logger := FromContext(context.Background(), base)
	if logger != base {
		t.Fatal("expected base logger when correlation is missing")
	}
}

func TestFromContextAttachesCorrelation(t *testing.T) {
	base := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	logger := FromContext(WithCorrelation(context.Background(), "req-1"), base)
	if logger == base {
		t.Fatal("expected derived logger when correlation is present")
	}
}
