package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func RegisterAuditTools(registry *Registry, audit MCPAuditStore) error {
	if registry == nil {
		return errors.New("mcp: registry is required")
	}
	if audit == nil {
		return errors.New("mcp: audit repository is required")
	}
	return registry.Register(&Tool{
		Name:           "get_audit_log",
		Description:    "Return durable MCP mutation and publication audit records in reverse chronological order.",
		RequiredScopes: []Scope{ScopeAudit},
		InputSchema: InputSchema{Properties: map[string]*InputSchemaProperty{
			"service_token_id": {Type: "string"}, "tool": {Type: "string"}, "target_type": {Type: "string"},
			"target_id": {Type: "string"}, "outcome": {Type: "string", Enum: []string{"success", "failure", "rejected"}},
			"from": {Type: "string"}, "to": {Type: "string"}, "limit": {Type: "integer"}, "offset": {Type: "integer"},
		}},
		Handler: func(ctx *Context, raw json.RawMessage) (any, error) {
			filter, fields, err := decodeMCPAuditFilter(raw)
			if err != nil {
				return nil, err
			}
			if len(fields) != 0 {
				return nil, NewToolError("invalid_args", "filter failed validation", fields)
			}
			items, err := audit.ListMCPAudit(ctx.Ctx, filter)
			if err != nil {
				return nil, fmt.Errorf("get_audit_log: %w", err)
			}
			return map[string]any{"events": items, "count": len(items), "limit": filter.Limit, "offset": filter.Offset}, nil
		},
	})
}

func decodeMCPAuditFilter(raw json.RawMessage) (MCPAuditFilter, map[string]string, error) {
	var args struct {
		ServiceTokenID string `json:"service_token_id"`
		Tool           string `json:"tool"`
		TargetType     string `json:"target_type"`
		TargetID       string `json:"target_id"`
		Outcome        string `json:"outcome"`
		From           string `json:"from"`
		To             string `json:"to"`
		Limit          int    `json:"limit"`
		Offset         int    `json:"offset"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return MCPAuditFilter{}, nil, err
	}
	fields := map[string]string{}
	if args.Limit == 0 {
		args.Limit = 50
	}
	if args.Limit < 1 || args.Limit > 200 {
		fields["limit"] = "must be between 1 and 200"
	}
	if args.Offset < 0 {
		fields["offset"] = "must be non-negative"
	}
	if args.Outcome != "" && args.Outcome != "success" && args.Outcome != "failure" && args.Outcome != "rejected" {
		fields["outcome"] = "must be success, failure, or rejected"
	}
	filter := MCPAuditFilter{ServiceTokenID: args.ServiceTokenID, Tool: args.Tool, TargetType: args.TargetType, TargetID: args.TargetID, Outcome: args.Outcome, Limit: args.Limit, Offset: args.Offset}
	if args.From != "" {
		value, err := time.Parse(time.RFC3339, args.From)
		if err != nil {
			fields["from"] = "must be an RFC3339 timestamp"
		} else {
			filter.From = value
		}
	}
	if args.To != "" {
		value, err := time.Parse(time.RFC3339, args.To)
		if err != nil {
			fields["to"] = "must be an RFC3339 timestamp"
		} else {
			filter.To = value
		}
	}
	return filter, fields, nil
}
