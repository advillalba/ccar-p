# MCP integration

The platform exposes a remote Model Context Protocol endpoint at
`/mcp`. It is logically isolated from `/api/v1` and requires a bearer
service token for every request. Publish, archive, export, and content
mutation tools are intentionally available to scoped automation.

## Authentication and rotation

Clients send a token with:

```
Authorization: Bearer <token>
```

The server SHA-256 hashes the presented token before comparison. The
bootstrap token is configured as the SHA-256 hex digest in
`MCP_SERVICE_TOKEN_HASH`; it receives all scopes and is intended only
for initial token management. Database-backed service tokens retain
only their hash, scopes, expiry, revocation timestamp, and last-used
time. Plaintext tokens are returned once when created and must be
stored in a secret manager.

To rotate access, create a replacement token with the least required
scopes, update the client, then revoke the old token. Rotate the
bootstrap secret by replacing `MCP_SERVICE_TOKEN_HASH` and restarting
the backend. Never place bearer values in source, logs, or audit
metadata.

Expired, revoked, missing, malformed, and mismatched tokens are denied.
The endpoint is rate-limited per authenticated token and returns retry
information when the limit is reached.

## Scopes and tools

| Scope | Tools |
|---|---|
| `notes:read` | `list_domains`, `list_notes`, `get_note` |
| `notes:write` | `create_note`, `update_note`, `preview_note` |
| `notes:publish` | `publish_note`, `archive_note` |
| `exams:read` | `list_exams`, `get_exam` |
| `exams:write` | `create_exam`, `update_exam`, `create_question`, `update_question`, `delete_question`, `reorder_questions`, `validate_exam` |
| `exams:publish` | `publish_exam`, `archive_exam` |
| `audit:read` | `get_publication_status`, `get_audit_log` |

Publication tools accept dry-run requests. Dry runs return blockers,
warnings, and a prospective version without changing state, events,
or caches. Real publication and archival are
audited with the authenticated token identity, never its bearer value.

## Boundaries

`get_publication_status` reports the recorded outcome. The MCP surface
has no arbitrary SQL, filesystem, secret-management, or user-mutation
tools.

Logs use request correlation and secret redaction. Metrics are exposed
only on the private backend network. Keep the public reverse proxy
limited to the frontend unless an explicitly authenticated MCP ingress
is required.
