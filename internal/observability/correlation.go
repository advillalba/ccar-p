package observability

import (
	"context"
	"log/slog"
)

// correlationKey is the context key used to attach a request correlation
// identifier. It is exported only via WithCorrelation and Correlation so
// the rest of the codebase does not need to know about the underlying
// key type.
type correlationKey struct{}

// WithCorrelation returns a copy of ctx that carries the supplied
// correlation identifier. Empty values are ignored.
func WithCorrelation(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// Correlation returns the correlation identifier stored in ctx. When no
// identifier is present, the empty string is returned.
func Correlation(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(correlationKey{}).(string)
	return value
}

// CorrelationHandler decorates an slog.Handler so every record emitted
// with the supplied context includes the correlation identifier as a
// `request_id` attribute. The wrapped handler is responsible for the
// eventual redaction step.
type CorrelationHandler struct {
	inner slog.Handler
}

// NewCorrelationHandler wraps inner with correlation injection.
func NewCorrelationHandler(inner slog.Handler) *CorrelationHandler {
	if inner == nil {
		inner = slog.NewJSONHandler(discardWriter{}, nil)
	}
	return &CorrelationHandler{inner: inner}
}

// Enabled implements slog.Handler.
func (h *CorrelationHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler by attaching the correlation identifier
// to the record before delegating to the inner handler.
func (h *CorrelationHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := Correlation(ctx); id != "" {
		record.AddAttrs(slog.String("request_id", id))
	}
	return h.inner.Handle(ctx, record)
}

// WithAttrs implements slog.Handler.
func (h *CorrelationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CorrelationHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup implements slog.Handler.
func (h *CorrelationHandler) WithGroup(name string) slog.Handler {
	return &CorrelationHandler{inner: h.inner.WithGroup(name)}
}

// ChainLogger wraps inner with correlation injection and secret
// redaction. It is the recommended slog handler for the HTTP, database,
// publication, and MCP subsystems.
func ChainLogger(inner slog.Handler, opts *RedactingOptions) slog.Handler {
	if inner == nil {
		inner = slog.NewJSONHandler(discardWriter{}, nil)
	}
	return NewCorrelationHandler(NewRedactingHandler(inner, opts))
}

// FromContext returns a logger derived from base that carries the
// correlation identifier attached to ctx. When the context does not
// carry an identifier, base is returned unchanged.
func FromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		return slog.Default()
	}
	if id := Correlation(ctx); id != "" {
		return base.With("request_id", id)
	}
	return base
}
