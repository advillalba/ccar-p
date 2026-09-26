package observability

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

// SensitiveKeySubstrings lists substrings whose attribute values must never
// appear in logs. Matches are case-insensitive.
var SensitiveKeySubstrings = []string{
	"password",
	"secret",
	"token",
	"session",
	"csrf",
	"cookie",
	"authorization",
	"hash",
	"passphrase",
	"credential",
	"apikey",
	"api_key",
	"private",
}

// SensitiveValuePatterns matches secret-bearing values such as JWTs,
// API tokens, password query parameters, session cookies, and
// PostgreSQL connection strings. The patterns are intentionally broad
// to err on the side of redaction.
var SensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)postgres(?:ql)?://[^:\s]+:[^@\s]+@\S+`),
	regexp.MustCompile(`(?i)\bSESSION_SECRET=[^\s&]+`),
	regexp.MustCompile(`(?i)Authorization:\s*[^\s,;]+`),
	regexp.MustCompile(`(?i)(?:^|;|\s)(?:ccarp_)?session=[^;\s]+`),
	regexp.MustCompile(`(?i)(?:^|;|\s)(?:ccarp_)?csrf=[^;\s]+`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-+/=]+`),
}

// RedactingHandler is an slog.Handler that strips secrets from log records
// before delegating to the inner handler. The handler performs redaction
// across the request, attributes, and any grouped attributes attached at
// the call site.
type RedactingHandler struct {
	inner          slog.Handler
	keyRedactor    func(string) bool
	valueRedactors []*regexp.Regexp
}

// RedactingOptions configures the redaction handler.
type RedactingOptions struct {
	// Keys, when non-empty, replaces SensitiveKeySubstrings.
	Keys []string
	// ExtraPatterns is appended to SensitiveValuePatterns.
	ExtraPatterns []*regexp.Regexp
	// Inner is the wrapped handler that receives the redacted records.
	Inner slog.Handler
}

// NewRedactingHandler wraps the provided inner handler with secret
// redaction. When opts is nil, the default key list and value patterns
// are applied.
func NewRedactingHandler(inner slog.Handler, opts *RedactingOptions) *RedactingHandler {
	if inner == nil {
		inner = slog.NewJSONHandler(discardWriter{}, nil)
	}
	keys := SensitiveKeySubstrings
	patterns := append([]*regexp.Regexp{}, SensitiveValuePatterns...)
	if opts != nil {
		if len(opts.Keys) > 0 {
			keys = opts.Keys
		}
		if len(opts.ExtraPatterns) > 0 {
			patterns = append(patterns, opts.ExtraPatterns...)
		}
		if opts.Inner != nil {
			inner = opts.Inner
		}
	}
	normalized := make([]string, 0, len(keys))
	for _, key := range keys {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(key)))
	}
	return &RedactingHandler{
		inner:          inner,
		keyRedactor:    buildKeyRedactor(normalized),
		valueRedactors: patterns,
	}
}

// Enabled implements slog.Handler.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler by redacting the record before passing
// it to the inner handler. A redacted clone is used so concurrent
// callers cannot mutate shared state.
func (h *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clone := slog.Record{
		Time:    record.Time,
		Message: RedactValue(record.Message, h.valueRedactors),
		Level:   record.Level,
		PC:      record.PC,
	}
	record.Attrs(func(attr slog.Attr) bool {
		clone.AddAttrs(h.redactAttr(attr))
		return true
	})
	return h.inner.Handle(ctx, clone)
}

// WithAttrs implements slog.Handler.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		redacted = append(redacted, h.redactAttr(attr))
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(redacted), keyRedactor: h.keyRedactor, valueRedactors: h.valueRedactors}
}

// WithGroup implements slog.Handler.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name), keyRedactor: h.keyRedactor, valueRedactors: h.valueRedactors}
}

func (h *RedactingHandler) redactAttr(attr slog.Attr) slog.Attr {
	if h.keyRedactor(attr.Key) {
		return slog.String(attr.Key, "[REDACTED]")
	}
	switch attr.Value.Kind() {
	case slog.KindGroup:
		children := attr.Value.Group()
		redacted := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			redacted = append(redacted, h.redactAttr(child))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}
	case slog.KindLogValuer:
		resolved := attr.Value.Resolve()
		if resolved.Kind() == slog.KindGroup {
			return h.redactAttr(slog.Attr{Key: attr.Key, Value: resolved})
		}
		return slog.Attr{Key: attr.Key, Value: slog.StringValue(RedactValue(attr.Value.String(), h.valueRedactors))}
	default:
		return slog.Attr{Key: attr.Key, Value: slog.StringValue(RedactValue(attr.Value.String(), h.valueRedactors))}
	}
}

// RedactValue applies the configured value patterns to the input.
func RedactValue(input string, patterns []*regexp.Regexp) string {
	if input == "" {
		return input
	}
	for _, pattern := range patterns {
		input = pattern.ReplaceAllString(input, "[REDACTED]")
	}
	return input
}

func buildKeyRedactor(keys []string) func(string) bool {
	return func(name string) bool {
		lower := strings.ToLower(strings.TrimSpace(name))
		for _, key := range keys {
			if key == "" {
				continue
			}
			if strings.Contains(lower, key) {
				return true
			}
		}
		return false
	}
}

// discardWriter discards everything written to it; it is used when no
// inner handler is supplied so redaction does not panic.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
