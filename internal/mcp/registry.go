package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Scope names an authorization scope that gates access to one or more tools.
// Service tokens carry a list of scopes; the registry enforces that every
// required scope for the tool being invoked is present.
type Scope string

const (
	ScopeDomainsRead  Scope = "domains:read"
	ScopeDomainsWrite Scope = "domains:write"
	ScopeNotesRead    Scope = "notes:read"
	ScopeNotesWrite   Scope = "notes:write"
	ScopeNotesPublish Scope = "notes:publish"
	ScopeExamsRead    Scope = "exams:read"
	ScopeExamsWrite   Scope = "exams:write"
	ScopeExamsPublish Scope = "exams:publish"
	ScopeAudit        Scope = "audit:read"
)

// Scopes is a deduplicated set of scope strings.
type Scopes map[Scope]struct{}

func AllScopes() Scopes {
	return Scopes{
		ScopeDomainsRead: {}, ScopeDomainsWrite: {},
		ScopeNotesRead: {}, ScopeNotesWrite: {}, ScopeNotesPublish: {},
		ScopeExamsRead: {}, ScopeExamsWrite: {}, ScopeExamsPublish: {},
		ScopeAudit: {},
	}
}

func ParseScopes(values []string) (Scopes, error) {
	allowed := AllScopes()
	out := Scopes{}
	for _, value := range values {
		scope := Scope(strings.TrimSpace(value))
		if !allowed.Has(scope) {
			return nil, fmt.Errorf("mcp: unknown scope %q", value)
		}
		out[scope] = struct{}{}
	}
	return out, nil
}

func (s Scopes) Clone() Scopes {
	out := make(Scopes, len(s))
	for scope := range s {
		out[scope] = struct{}{}
	}
	return out
}

// Has reports whether the supplied scope is present.
func (s Scopes) Has(scope Scope) bool {
	if s == nil {
		return false
	}
	_, ok := s[scope]
	return ok
}

// ContainsAll reports whether every required scope is present.
func (s Scopes) ContainsAll(required ...Scope) bool {
	for _, scope := range required {
		if !s.Has(scope) {
			return false
		}
	}
	return true
}

// List returns the scopes in sorted order for deterministic output.
func (s Scopes) List() []Scope {
	out := make([]Scope, 0, len(s))
	for scope := range s {
		out = append(out, scope)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ToolHandler executes a tool call. The context provides the authenticated
// actor and request-scoped state. Returning a ToolError surfaces a structured
// field-level failure; any other error is reported as a generic internal
// failure and must not leak sensitive details.
type ToolHandler func(ctx *Context, args json.RawMessage) (any, error)

// InputSchema describes the JSON shape of a tool's argument object. The
// registry does not enforce it; it is returned verbatim to the client during
// tools/list.
type InputSchema struct {
	Type                 string                          `json:"type"`
	Properties           map[string]*InputSchemaProperty `json:"properties,omitempty"`
	Required             []string                        `json:"required,omitempty"`
	AdditionalProperties bool                            `json:"additionalProperties"`
}

// InputSchemaProperty is a single field declaration inside an InputSchema.
type InputSchemaProperty struct {
	Type        string                          `json:"type"`
	Description string                          `json:"description,omitempty"`
	Enum        []string                        `json:"enum,omitempty"`
	Items       *InputSchemaProperty            `json:"items,omitempty"`
	Properties  map[string]*InputSchemaProperty `json:"properties,omitempty"`
	Required    []string                        `json:"required,omitempty"`
}

// Tool describes a single MCP tool exposed by the registry.
type Tool struct {
	Name           string
	Description    string
	RequiredScopes []Scope
	InputSchema    InputSchema
	Mutation       bool
	Publication    bool
	Handler        ToolHandler
}

// ToolError is a structured field-level failure returned from a handler.
// Fields maps argument names to human-readable messages.
type ToolError struct {
	Code    string
	Message string
	Fields  map[string]string
}

// Error implements the error interface.
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewToolError builds a ToolError with the supplied code and message.
func NewToolError(code, message string, fields map[string]string) *ToolError {
	cleaned := make(map[string]string, len(fields))
	for key, value := range fields {
		cleaned[key] = value
	}
	return &ToolError{Code: code, Message: message, Fields: cleaned}
}

func decodeStrict(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return NewToolError("invalid_args", "arguments must match the tool schema", map[string]string{"arguments": err.Error()})
	}
	if decoder.More() {
		return NewToolError("invalid_args", "arguments must contain one JSON object", map[string]string{"arguments": "multiple JSON values"})
	}
	return nil
}

// Registry stores MCP tools keyed by name. It is safe for concurrent reads;
// registrations should occur during construction.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	order []string
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]*Tool)}
}

// Register adds a tool to the registry. Duplicate names cause an error so
// typos surface immediately during construction.
func (r *Registry) Register(tool *Tool) error {
	if tool == nil {
		return errors.New("mcp: tool is nil")
	}
	if tool.Name == "" {
		return errors.New("mcp: tool name is required")
	}
	if tool.Handler == nil {
		return fmt.Errorf("mcp: tool %q has no handler", tool.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.Name]; exists {
		return fmt.Errorf("mcp: tool %q registered twice", tool.Name)
	}
	if tool.InputSchema.Type == "" {
		tool.InputSchema.Type = "object"
	}
	if strings.HasPrefix(tool.Name, "create_") || strings.HasPrefix(tool.Name, "update_") || strings.HasPrefix(tool.Name, "delete_") || strings.HasPrefix(tool.Name, "reorder_") || strings.HasPrefix(tool.Name, "publish_") || strings.HasPrefix(tool.Name, "archive_") || strings.HasPrefix(tool.Name, "export_") {
		tool.Mutation = true
	}
	if strings.HasPrefix(tool.Name, "publish_") || strings.HasPrefix(tool.Name, "archive_") || strings.HasPrefix(tool.Name, "export_") || tool.Name == "get_publication_status" {
		tool.Publication = true
	}
	r.tools[tool.Name] = tool
	r.order = append(r.order, tool.Name)
	return nil
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// Names returns the registered tool names in insertion order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Spec returns the JSON-RPC representation of the tool for tools/list.
func (r *Registry) Spec(name string) (map[string]any, bool) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, false
	}
	return map[string]any{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
	}, true
}

// Authorize checks that the supplied scopes satisfy the tool's required
// scopes. The required scopes slice is treated as AND.
func (r *Registry) Authorize(name string, scopes Scopes) error {
	tool, ok := r.Get(name)
	if !ok {
		return fmt.Errorf("mcp: tool %q is not registered", name)
	}
	if !scopes.ContainsAll(tool.RequiredScopes...) {
		return fmt.Errorf("mcp: token is missing required scopes for %q", name)
	}
	return nil
}
