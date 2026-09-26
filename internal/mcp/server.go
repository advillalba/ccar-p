package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ccar-p/study-platform/internal/observability"
	"github.com/ccar-p/study-platform/internal/publishing"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultRequestTimeout = 20 * time.Second
	defaultRateLimit      = 6000
	defaultRateWindow     = time.Minute
)

type ToolMeta struct {
	Actor     publishing.Actor `json:"actor"`
	RequestID string           `json:"request_id"`
}

type Principal struct {
	Actor           publishing.Actor
	ContentAuthorID string
	Scopes          Scopes
	Bootstrap       bool
}

type Context struct {
	Ctx             context.Context
	Request         *http.Request
	Logger          *slog.Logger
	Actor           publishing.Actor
	ContentAuthorID string
	Scopes          Scopes
	RequestID       string
}

type AuditEvent struct {
	RequestID  string
	Actor      publishing.Actor
	Tool       string
	TargetType string
	TargetID   string
	Outcome    string
	ErrorCode  string
	Duration   time.Duration
	CreatedAt  time.Time
}

type AuditRepository interface {
	RecordMCPAudit(context.Context, AuditEvent) error
}

type Server struct {
	registry   *Registry
	auth       *TokenAuthenticator
	logger     *slog.Logger
	metrics    *observability.Metrics
	audit      AuditRepository
	timeout    time.Duration
	rateLimit  int
	rateWindow time.Duration
	now        func() time.Time
	limiter    *tokenLimiter
	handler    http.Handler
	name       string
	version    string
}

type ServerOption func(*Server)

func WithMetrics(metrics *observability.Metrics) ServerOption {
	return func(server *Server) { server.metrics = metrics }
}

func WithAuditRepository(audit AuditRepository) ServerOption {
	return func(server *Server) { server.audit = audit }
}

func WithTimeout(timeout time.Duration) ServerOption {
	return func(server *Server) { server.timeout = timeout }
}

func WithRateLimit(limit int, window time.Duration) ServerOption {
	return func(server *Server) {
		server.rateLimit = limit
		server.rateWindow = window
	}
}

func WithServerInfo(name, version string) ServerOption {
	return func(server *Server) {
		server.name = name
		server.version = version
	}
}

func NewServer(registry *Registry, auth *TokenAuthenticator, logger *slog.Logger, options ...ServerOption) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{
		registry: registry, auth: auth, logger: logger, metrics: observability.NewMetrics(),
		timeout: defaultRequestTimeout, rateLimit: defaultRateLimit, rateWindow: defaultRateWindow,
		now: time.Now, name: "ccarp-mcp", version: "1.0.0",
	}
	for _, option := range options {
		option(server)
	}
	server.limiter = newTokenLimiter(server.rateLimit, server.rateWindow, server.now)
	server.handler = server.buildHandler()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) buildHandler() http.Handler {
	protocol := sdk.NewServer(&sdk.Implementation{Name: s.name, Version: s.version}, nil)
	for _, name := range s.registry.Names() {
		tool, ok := s.registry.Get(name)
		if !ok {
			continue
		}
		registered := tool
		protocol.AddTool(&sdk.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return s.callTool(ctx, registered, request)
		})
	}
	streamable := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return protocol }, &sdk.StreamableHTTPOptions{
		JSONResponse: true, Stateless: true, Logger: s.logger,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestID := ensureRequestID(request)
		w.Header().Set("X-Request-ID", requestID)
		principal, err := s.auth.authenticate(request.Context(), request)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			if s.logger != nil {
				s.logger.Debug("mcp authentication failed", "error", err)
			}
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		retry, blocked := s.limiter.check(actorKey(principal.Actor), s.now())
		if blocked {
			if s.metrics != nil {
				s.metrics.RecordThrottle("mcp")
			}
			seconds := int(retry.Round(time.Second).Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "rate_limited", "message": "rate limit exceeded", "retry_after_seconds": seconds, "request_id": requestID}})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
		defer cancel()
		ctx = context.WithValue(ctx, principalKey{}, principal)
		ctx = context.WithValue(ctx, requestKey{}, request)
		ctx = context.WithValue(ctx, requestIDKey{}, requestID)
		streamable.ServeHTTP(w, request.WithContext(ctx))
	})
}

func (s *Server) callTool(ctx context.Context, tool *Tool, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	if !ok {
		return toolFailure("unauthorized", "authentication required", nil), nil
	}
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	started := s.now()
	if !principal.Scopes.ContainsAll(tool.RequiredScopes...) {
		s.recordAudit(ctx, tool, principal.Actor, requestID, "rejected", "missing_scope", request.Params.Arguments, started)
		return toolFailure("missing_scope", "missing required scopes", map[string]string{"tool": tool.Name}), nil
	}
	result, err := tool.Handler(&Context{Ctx: ctx, Request: requestFromContext(ctx), Logger: s.logger, Actor: principal.Actor, ContentAuthorID: principal.ContentAuthorID, Scopes: principal.Scopes, RequestID: requestID}, request.Params.Arguments)
	if err != nil {
		var toolError *ToolError
		if errors.As(err, &toolError) {
			s.recordAudit(ctx, tool, principal.Actor, requestID, "failure", toolError.Code, request.Params.Arguments, started)
			return toolFailure(toolError.Code, toolError.Message, toolError.Fields), nil
		}
		s.logger.Error("mcp tool failed", "request_id", requestID, "tool", tool.Name, "error", err.Error())
		s.recordAudit(ctx, tool, principal.Actor, requestID, "failure", "internal_error", request.Params.Arguments, started)
		return toolFailure("internal_error", "internal server error", nil), nil
	}
	s.recordAudit(ctx, tool, principal.Actor, requestID, "success", "", request.Params.Arguments, started)
	structured := map[string]any{"data": result, "meta": ToolMeta{Actor: principal.Actor, RequestID: requestID}}
	encoded, _ := json.Marshal(structured)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(encoded)}}, StructuredContent: structured}, nil
}

func toolFailure(code, message string, fields map[string]string) *sdk.CallToolResult {
	payload := map[string]any{"error": map[string]any{"code": code, "message": message, "fields": fields}}
	encoded, _ := json.Marshal(payload)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(encoded)}}, StructuredContent: payload, IsError: true}
}

func (s *Server) recordAudit(ctx context.Context, tool *Tool, actor publishing.Actor, requestID, outcome, errorCode string, args json.RawMessage, started time.Time) {
	if s.audit == nil || (!tool.Mutation && !tool.Publication) {
		return
	}
	targetType, targetID := auditTarget(tool.Name, args)
	event := AuditEvent{RequestID: requestID, Actor: actor, Tool: tool.Name, TargetType: targetType, TargetID: targetID, Outcome: outcome, ErrorCode: errorCode, Duration: s.now().Sub(started), CreatedAt: s.now().UTC()}
	if err := s.audit.RecordMCPAudit(ctx, event); err != nil {
		s.logger.Error("mcp audit persistence failed", "request_id", requestID, "tool", tool.Name, "error", err.Error())
	}
}

func auditTarget(tool string, raw json.RawMessage) (string, string) {
	var args struct {
		ID     string `json:"id"`
		ExamID string `json:"exam_id"`
	}
	_ = json.Unmarshal(raw, &args)
	switch {
	case strings.Contains(tool, "domain"):
		return "domain", args.ID
	case strings.Contains(tool, "note"):
		return "note", args.ID
	case strings.Contains(tool, "exam") || strings.Contains(tool, "question"):
		if args.ID != "" {
			return "exam", args.ID
		}
		return "exam", args.ExamID
	default:
		return "", args.ID
	}
}

func requestFromContext(ctx context.Context) *http.Request {
	request, _ := ctx.Value(requestKey{}).(*http.Request)
	return request
}

func actorKey(actor publishing.Actor) string {
	return string(actor.Kind) + ":" + actor.ID
}

func ensureRequestID(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if len(value) >= 8 && len(value) <= 128 {
		return value
	}
	return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
}

type tokenLimiter struct {
	mu      sync.Mutex
	entries map[string]tokenLimit
	limit   int
	window  time.Duration
	now     func() time.Time
	max     int
}

type tokenLimit struct {
	count int
	reset time.Time
}

func newTokenLimiter(limit int, window time.Duration, now func() time.Time) *tokenLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &tokenLimiter{entries: make(map[string]tokenLimit), limit: limit, window: window, now: now, max: 4096}
}

func (l *tokenLimiter) check(key string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || !entry.reset.After(now) {
		if len(l.entries) >= l.max {
			for existing, candidate := range l.entries {
				if !candidate.reset.After(now) {
					delete(l.entries, existing)
				}
			}
		}
		l.entries[key] = tokenLimit{count: 1, reset: now.Add(l.window)}
		return 0, false
	}
	if entry.count >= l.limit {
		return entry.reset.Sub(now), true
	}
	entry.count++
	l.entries[key] = entry
	return 0, false
}

type principalKey struct{}
type requestKey struct{}
type requestIDKey struct{}
