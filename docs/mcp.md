# Native MCP API

NoX Backend exposes **POST `/mcp`** through the official
[NoX MCP Go runtime](https://github.com/mapherez/nox-mcp/tree/v0.4.1/go), using
the published `github.com/mapherez/nox-mcp v0.4.1` module. It runs in the same
Go process, HTTP server, bind address and port as the existing API and dashboard.
There is no second service or port. Configure a Streamable HTTP MCP client with
the backend's base URL plus `/mcp`, retaining any reverse-proxy path prefix.

Responses use JSON request/response mode, without a persistent SSE stream or
server-side MCP session. Clients should disable standalone SSE. Non-POST
requests to `/mcp` return `405` with `Allow: POST`. The runtime implements the
MCP protocol, including `initialize`, `tools/list` and `tools/call`; the backend
does not implement its own MCP transport.

The MCP identity is `AppID: nox-backend`, `Name: nox-backend`. Its version is
the backend's already-resolved runtime version, exactly as in `/v1/health` and
`/v1/info`. No separate version configuration is introduced. Existing service
capabilities remain `auth`, `vaults`, `files`, `vault-status`.

## Authentication and ownership

Use the existing per-user API key from `/vault-dashboard`:

```http
Authorization: Bearer <API_KEY>
```

Credentials belong to the HTTP request, never tool arguments. No MCP-specific
credential, JWT or OAuth flow is introduced. Dashboard cookies do not authenticate
MCP calls. The legacy `api_key` query parameter follows the HTTP API's existing
behavior, including Bearer precedence; prefer the header to keep secrets out of
URLs.

The backend validates credentials against the existing storage on every HTTP
request. A small request-context adapter passes the authenticated user or the
authentication failure into tool execution. The SDK's mandatory Bearer middleware
cannot preserve both public tools and structured per-tool authentication errors.
The adapter retains the resolved identity/error, not the API key.

Initialization and catalog listing are public. `backend_health` and
`backend_info` do not require authentication and ignore invalid credentials, as
their HTTP equivalents do. The other eight tools require authentication:

- Missing API key: `AUTH_REQUIRED`.
- Invalid or rotated key, or disabled user: `AUTH_FAILED`.
- Internal authentication failure: `SERVER_ERROR`, with a safe message.

Each vault operation is restricted to the key owner's vaults. `ADMIN` grants
no access to other users' vaults. Missing, inaccessible or incompatible-state
vaults produce `NOT_FOUND` without revealing ownership. Authentication is
rechecked after key rotation or account disabling, even for an initialized client.

## Tools and CLI metadata

Exactly these 10 canonical tools are exposed. Every tool has an input schema,
output schema, description, annotations and `_meta.cli`.

| Tool | `_meta.cli` | Input | Read-only | Destructive | Idempotent |
| --- | --- | --- | --- | --- | --- |
| `backend_health` | `health` | `{}` | Yes | No | Yes |
| `backend_info` | `info` | `{}` | Yes | No | Yes |
| `backend_whoami` | `whoami` | `{}` | Yes | No | Yes |
| `backend_vault_list` | `vault list` | `{}` | Yes | No | Yes |
| `backend_vault_create` | `vault create` | `{"name":"My Vault"}` | No | No | No |
| `backend_vault_delete` | `vault delete` | `{"vaultId":"vault_example"}` | No | Yes | No |
| `backend_vault_restore` | `vault restore` | `{"vaultId":"vault_example"}` | No | No | No |
| `backend_vault_purge` | `vault purge` | `{"vaultId":"vault_example"}` | No | Yes | No |
| `backend_vault_status` | `vault status` | `{"vaultId":"vault_example"}` | Yes | No | Yes |
| `backend_file_list` | `file list` | `{"vaultId":"vault_example"}` | Yes | No | Yes |

CLI paths are app-local: they contain no `backend ` prefix. For example,
`backend_vault_list` appears in `tools/list` with:

```json
{"_meta":{"cli":"vault list"}}
```

A future CLI can compose `nox backend vault list`. This repository does not
implement that CLI. A single explicit mapping supplies `_meta.cli` through the
official runtime's `Meta` field. Runtime construction fails clearly if a
registered tool lacks a unique app-local mapping.

## Results and Stable Client API behavior

Results are structured JSON in MCP `structuredContent`, with equivalent JSON
text content for compatibility. These tools represent the
[Stable Client API](client-api.md) and use its real storage and shared helpers
directly, without internal requests to `/v1`.

- Health: `status`, `version`, `dataDirInitialized`, `databasePath`, preserving
  the existing logical readiness signal and configured database path.
- Info: `service`, `version`, `apiVersion`, `capabilities`.
- Whoami: `user` (email), `role`; no API key or legacy `vault` marker. The
  HTTP identity response remains unchanged.
- Vault list: `vaults` and optional `deletedVaults`, using the existing vault
  fields, lifecycle states and ordering. Empty active lists are `[]`;
  `deletedVaults` is omitted when empty.
- Create: the created vault object. Storage trims names, rejects empty names
  and enforces the existing 160-byte limit, including for Unicode names.
- Delete, restore and purge: `{"ok":true}`. Delete is reversible soft-delete;
  purge irreversibly removes a deleted vault through `PurgeDeletedVault`,
  preserving the existing removal of unreferenced blobs and retention of shared
  blobs. Repeating lifecycle operations in the wrong state gives `NOT_FOUND`.
- Status: `vaultId`, `serverRevision`, `sync`. Although read-only from the user's
  perspective, it preserves the existing stale-lock refresh/reap and associated
  status broadcast through the same helper as `/v1/status`.
- File list: `vaultId`, `serverRevision`, `files`. Files retain `path`, `hash`,
  `size`, `revision`, path ordering and committed-state semantics. Staged uploads,
  tombstones, content and internal host paths are excluded. This read does not
  refresh locks or emit sync events. See the [read API](read-api.md) for details.

Revisions and byte sizes retain their signed integer precision in the JSON wire
result, including values above the exact integer range of a floating-point number.

Known backend failures use `noxmcp.Error` with stable codes `BAD_REQUEST`,
`NOT_FOUND`, `FILE_CHANGED` (when applicable), `AUTH_REQUIRED`, `AUTH_FAILED`
and `SERVER_ERROR`. Failed calls set `isError: true` and contain `code`,
`message`, `retryable` (false for these backend failures). Unexpected internal
failures return a generic message without SQL, paths, stack traces or secrets.
MCP errors do not wrap HTTP responses.

The official NoX MCP runtime validates schemas and enforces its own execution
policies. Schema-invalid arguments, including unknown properties, return its
`INVALID_INPUT` code; validly shaped inputs that fail backend validation return
`BAD_REQUEST`. Runtime failures such as `TIMEOUT`, `CANCELLED`, `OVERLOADED` and
`INTERNAL` retain the official runtime's semantics. A timed-out mutation may have
an unknown outcome; clients should inspect the current state before retrying.

No limits are overridden: the published runtime defaults are 20 seconds per
execution, 2 MiB maximum payload and 32 concurrent in-flight executions. Large
structured results remain subject to that payload limit; no pagination or new
business endpoint is introduced here.

## Deliberate exclusions

Binary file download remains available at **`/v1/files/download`**. There is no
download tool, base64 encoding or chunking protocol. File listing tools return
metadata so clients can use the existing HTTP download contract.

The specialized **`/v1/sync/*`** protocol stays separate and unchanged. MCP does
not expose sync orchestration, uploads, continuous streaming or SSE. Dashboard
and user administration, discovery (including mDNS and `/.well-known/nox`),
new authentication systems and CLI implementation are outside this MCP API.
Existing HTTP sync SSE remains available for its existing clients.

## Verification

Run `go test ./...` from `backend/`. MCP integration tests use the official Go
MCP SDK over a real `httptest.NewServer` and temporary SQLite/blob storage. They
cover initialization, wire catalog metadata/schemas/annotations, tool calls,
public access, authentication/rotation/disabled users, ownership including
admins, lifecycle behavior shared with HTTP, committed file reads, stale-lock
broadcasts and sanitized internal failures. The existing Go CI job includes
these tests automatically.
