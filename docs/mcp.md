# Direct MCP API

NoX Backend exposes **POST `/mcp`** (enabled by default) through the official
[NoX MCP Go runtime](https://github.com/mapherez/nox-mcp/tree/v0.7.3/go), using
the published `github.com/mapherez/nox-mcp v0.7.3` module. It runs in the same
Go process, HTTP server, bind address and port as the existing backend APIs.
The separate React frontend forwards `/mcp` unchanged on the public origin; it
does not implement MCP or hold user/service authority. Configure a Streamable HTTP MCP client with
the backend's base URL plus `/mcp`. The separate protected
[Suite MCP adapter](suite-product.md) uses `/mcp/suite` and its own token policy.
`NOX_BACKEND_DIRECT_MCP_ENABLED=false` disables only this direct endpoint.
Suite origins do not support reverse-proxy application prefixes.

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
output schema, description and annotations. Canonical source definitions also retain
`_meta.cli`; standard `tools/list` omits it through SDK 0.7.3.

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
`backend_vault_list` retains `{"_meta":{"cli":"vault list"}}` in its source
definition. Standard `tools/list` omits this CLI metadata, so ordinary MCP clients
receive the tool names, schemas, descriptions and annotations without menu data.
The Suite adapter's `nox/tools/definitions` retains the canonical CLI metadata
for NoX clients. The direct `/mcp` endpoint does not enable NoX discovery.

A future CLI can compose `nox backend vault list`. This repository does not
implement that CLI. A single explicit mapping supplies `_meta.cli` through the
official runtime's `Meta` field. Runtime construction fails clearly if a
registered tool lacks a unique app-local mapping.

## Human menus through NoX discovery

The Suite adapter publishes version `1.0` CLI descriptors for the same ten tools
through `nox/tools/definitions`. They are separate from the canonical tool and
its app-local `_meta.cli` path. The standard catalog has no menu descriptors.

```text
NoX Backend
├── Estado do serviço
├── Informações do serviço
├── Conta atual
├── Vaults
│   ├── Listar
│   ├── Criar
│   ├── Consultar estado
│   ├── Eliminar
│   ├── Restaurar
│   └── Eliminar definitivamente
└── Ficheiros
    └── Listar
```

The existing canonical paths stay `health`, `info`, `whoami`, `vault list`,
`vault create`, `vault status`, `vault delete`, `vault restore`, `vault purge`
and `file list`. Descriptor `navigation` supplies the human labels, groups
and order. The product name comes from its existing declaration. The Suite
runtime still filters both catalogs by the caller's existing scopes; readers
receive their five inspect tools and administrators receive all ten.

- **Criar:** a text input supplies the existing `/name` argument. Required fields
  and validation come from the unchanged input schema.
- **Consultar estado / Eliminar / Ficheiros → Listar:** select from the existing
  `backend_vault_list` result at `/vaults`.
- **Restaurar / Eliminar definitivamente:** select from `/deletedVaults` in that
  same result. An absent optional `deletedVaults` means no choices. An empty
  selector must not invoke a mutation; the human client can refresh or go back.
- Every selector uses `/name` as its label and `/vaultId` as its value relative
  to each row. Names are not unique: clients must preserve the IDs and display
  them when distinguishing equal names or reviewing a destructive operation.
- **Eliminar:** boolean confirmation explains that the data remains restorable.
  **Eliminar definitivamente:** typed confirmation matches the selected
  `/vaultId` and explicitly warns that the operation cannot be undone.

Selectors declare `requires: ["dynamic-options"]`; destructive menus additionally
require `confirmation`. There is no `bindPointer`, preview tool or new workflow:
confirmation does not add an argument, grant permission or change execution.
The client must not bypass an unsupported required interaction. It refreshes
choices after mutations; a stale selection is handled by the existing domain
errors, including `NOT_FOUND`, rather than being rebound to another vault.

Results retain the existing `structuredContent` and `outputSchema`. No custom
result-rendering fields or new output schemas are introduced. The future CLI
renders these results and the menus; this Backend change prepares and tests the
published data, without implementing a terminal UI. The descriptor factory and
wire/lifecycle tests are in `backend/internal/app/mcp_cli.go` and
`backend/internal/app/mcp_cli_test.go`.

## Results and Stable Client API behavior

Successful results are structured JSON in MCP `structuredContent`, with equivalent
JSON text content for compatibility. These tools represent the
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
and `SERVER_ERROR`. Failed calls set `isError: true` and carry their JSON body in
`content[].text`, without `structuredContent`, which is reserved for results that
match the success output schema. Clients must read errors from the text body,
which contains `code`, `message`, `retryable` (false for these backend failures).
Unexpected internal failures return a generic message without SQL, paths, stack
traces or secrets.
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
