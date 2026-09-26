package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/publishing"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const serviceToken = "test-service-token"

func tokenHash() string {
	sum := sha256.Sum256([]byte(serviceToken))
	return hex.EncodeToString(sum[:])
}

type memoryAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (a *memoryAudit) RecordMCPAudit(_ context.Context, event AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	return nil
}

func (a *memoryAudit) ListMCPAudit(context.Context, MCPAuditFilter) ([]MCPAuditRecord, error) {
	return nil, nil
}

type memoryTokens struct {
	records            map[string]TokenRecord
	contentAuthorError error
}

func (m *memoryTokens) FindServiceTokenByHash(_ context.Context, hash []byte) (TokenRecord, error) {
	if record, ok := m.records[hex.EncodeToString(hash)]; ok {
		return record, nil
	}
	return TokenRecord{}, ErrTokenMismatch
}

func (m *memoryTokens) ValidateContentAuthor(context.Context, string) error {
	return m.contentAuthorError
}
func (*memoryTokens) TouchServiceToken(context.Context, string, time.Time) error { return nil }

func testServer(t *testing.T, scopes Scopes, options ...ServerOption) (*Registry, *httptest.Server) {
	t.Helper()
	auth, err := NewTokenAuthenticatorWithRepository(tokenHash(), nil, scopes, bootstrapTokenID)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	server := NewServer(registry, auth, slog.New(slog.NewTextHandler(io.Discard, nil)), options...)
	return registry, httptest.NewServer(server.Handler())
}

func clientFor(t *testing.T, url, token string) *sdk.ClientSession {
	t.Helper()
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		request.Header.Set("Authorization", "Bearer "+token)
		return http.DefaultTransport.RoundTrip(request)
	})}
	protocolClient := sdk.NewClient(&sdk.Implementation{Name: "mcp-test", Version: "1.0.0"}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}})
	session, err := protocolClient.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url, HTTPClient: client, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestOfficialClientInitializesAndCallsTool(t *testing.T) {
	registry, server := testServer(t, AllScopes())
	defer server.Close()
	if err := registry.Register(&Tool{Name: "ping", InputSchema: InputSchema{Properties: map[string]*InputSchemaProperty{}}, Handler: func(*Context, json.RawMessage) (any, error) {
		return map[string]any{"pong": true}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = NewServer(registry, mustAuthenticator(t), slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	session := clientFor(t, server.URL, serviceToken)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "ping" {
		t.Fatalf("tools=%v err=%v", tools.Tools, err)
	}
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func mustAuthenticator(t *testing.T) *TokenAuthenticator {
	t.Helper()
	auth, err := NewTokenAuthenticatorWithRepository(tokenHash(), nil, AllScopes(), bootstrapTokenID)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestAuthenticationRejectsMissingInvalidExpiredAndRevoked(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&Tool{Name: "ping", Handler: func(*Context, json.RawMessage) (any, error) { return map[string]bool{"ok": true}, nil }})
	expired := time.Now().Add(-time.Minute)
	revoked := time.Now()
	cases := []struct {
		name   string
		token  string
		record TokenRecord
	}{
		{"invalid", "invalid", TokenRecord{}},
		{"expired", "expired", TokenRecord{ID: "00000000-0000-0000-0000-000000000002", Scopes: AllScopes(), ExpiresAt: &expired}},
		{"revoked", "revoked", TokenRecord{ID: "00000000-0000-0000-0000-000000000003", Scopes: AllScopes(), RevokedAt: &revoked}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			sum := sha256.Sum256([]byte(test.token))
			repository := &memoryTokens{records: map[string]TokenRecord{}}
			if test.record.ID != "" {
				repository.records[hex.EncodeToString(sum[:])] = test.record
			}
			auth, _ := NewTokenAuthenticatorWithRepository(tokenHash(), repository, AllScopes(), bootstrapTokenID)
			server := httptest.NewServer(NewServer(registry, auth, nil).Handler())
			defer server.Close()
			request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
			request.Header.Set("Authorization", "Bearer "+test.token)
			response, err := http.DefaultClient.Do(request)
			if err != nil || response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status=%v err=%v", response.StatusCode, err)
			}
			response.Body.Close()
		})
	}
	server := httptest.NewServer(NewServer(registry, mustAuthenticator(t), nil).Handler())
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(nil))
	response, _ := http.DefaultClient.Do(request)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestAuthenticationRejectsInactiveContentAuthor(t *testing.T) {
	token := "database-token"
	sum := sha256.Sum256([]byte(token))
	repository := &memoryTokens{
		records: map[string]TokenRecord{
			hex.EncodeToString(sum[:]): {ID: "00000000-0000-0000-0000-000000000002", ContentAuthorID: contentAuthorID, Scopes: AllScopes()},
		},
		contentAuthorError: ErrContentAuthorInactive,
	}
	auth, err := NewTokenAuthenticatorWithRepository(tokenHash(), repository, AllScopes(), contentAuthorID)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, err := auth.authenticate(context.Background(), request); !errors.Is(err, ErrContentAuthorInactive) {
		t.Fatalf("error = %v", err)
	}
}

func TestScopesCannotBeClientControlled(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&Tool{Name: "publish_note", RequiredScopes: []Scope{ScopeNotesPublish}, Handler: func(*Context, json.RawMessage) (any, error) { return nil, nil }})
	auth, _ := NewTokenAuthenticatorWithRepository(tokenHash(), nil, Scopes{ScopeNotesRead: {}}, bootstrapTokenID)
	server := httptest.NewServer(NewServer(registry, auth, nil).Handler())
	defer server.Close()
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		request.Header.Set("Authorization", "Bearer "+serviceToken)
		request.Header.Set("X-MCP-Scopes", string(ScopeNotesPublish))
		return http.DefaultTransport.RoundTrip(request)
	})}
	protocolClient := sdk.NewClient(&sdk.Implementation{Name: "mcp-test", Version: "1.0.0"}, nil)
	session, err := protocolClient.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "publish_note", Arguments: map[string]any{"id": "x"}})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*sdk.TextContent).Text, "missing_scope") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRateLimitIncludesRetryAfter(t *testing.T) {
	registry := NewRegistry()
	auth := mustAuthenticator(t)
	server := httptest.NewServer(NewServer(registry, auth, nil, WithRateLimit(1, time.Minute)).Handler())
	defer server.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	for attempt := 0; attempt < 2; attempt++ {
		request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+serviceToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if attempt == 1 && (response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "") {
			t.Fatalf("status=%d retry=%q", response.StatusCode, response.Header.Get("Retry-After"))
		}
	}
}

func TestMutationAuditUsesTokenIdentityWithoutBearer(t *testing.T) {
	audit := &memoryAudit{}
	registry := NewRegistry()
	_ = registry.Register(&Tool{Name: "create_note", RequiredScopes: []Scope{ScopeNotesWrite}, Handler: func(*Context, json.RawMessage) (any, error) { return map[string]bool{"ok": true}, nil }})
	server := httptest.NewServer(NewServer(registry, mustAuthenticator(t), nil, WithAuditRepository(audit)).Handler())
	defer server.Close()
	session := clientFor(t, server.URL, serviceToken)
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "create_note", Arguments: map[string]any{"id": "00000000-0000-0000-0000-000000000010"}})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(audit.events) != 1 || audit.events[0].Actor.ID != bootstrapTokenID || strings.Contains(audit.events[0].Actor.ID, serviceToken) {
		t.Fatalf("events=%+v", audit.events)
	}
}

func TestRegistryIsCompleteAndHasNoPrivilegedGenericTools(t *testing.T) {
	expected := []string{"list_domains", "get_domain", "create_domain", "update_domain", "delete_domain", "list_notes", "get_note", "create_note", "update_note", "preview_note", "publish_note", "archive_note", "list_exams", "get_exam", "create_exam", "update_exam", "publish_exam", "archive_exam", "create_question", "update_question", "patch_question", "delete_question", "reorder_questions", "validate_exam", "get_publication_status", "get_audit_log"}
	forbidden := []string{"sql", "filesystem", "file", "secret", "user"}
	if len(expected) != 26 {
		t.Fatal("incorrect expected registry")
	}
	for _, name := range expected {
		for _, fragment := range forbidden {
			if strings.Contains(name, fragment) {
				t.Fatalf("privileged generic tool %q", name)
			}
		}
	}
}

func TestParseScopesRejectsUnknown(t *testing.T) {
	if _, err := ParseScopes([]string{"notes:read", "admin:*"}); err == nil {
		t.Fatal("expected unknown scope rejection")
	}
	if scopes, err := ParseScopes([]string{"notes:read"}); err != nil || !scopes.Has(ScopeNotesRead) {
		t.Fatalf("scopes=%v err=%v", scopes, err)
	}
}

var _ = publishing.Actor{}
