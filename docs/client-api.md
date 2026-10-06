# Stable client HTTP API

This is the small, stable HTTP API provided by NoX Backend for external clients,
including a future standalone NoX CLI. Existing NoX Sync clients continue to use
their current contracts. This repository does not implement the CLI.

## Base URL and authentication

Configure the backend base URL, for example `https://backend.example.invalid`,
without appending `/v1`. Remove trailing slashes before appending the endpoint
paths below. When a reverse proxy exposes the backend under a path prefix,
retain that prefix and ensure the proxy forwards the corresponding routes.
URL-encode query parameter values, including vault IDs and file paths.

Authenticated endpoints use the per-user API key from `/vault-dashboard`:

```http
Authorization: Bearer <API_KEY>
```

The existing `noxsync_` key prefix is preserved. A rotated key or a key belonging
to a disabled user is rejected with `401` and `AUTH_FAILED`. Missing credentials
produce `401` and `AUTH_REQUIRED`.

The legacy `api_key` query parameter remains supported. A Bearer token takes
precedence over it. Use the Authorization header for new integrations so keys
do not appear in URLs. Dashboard cookies are not API-key authentication.

All vault operations are restricted to the authenticated user's vaults,
including for users with role `ADMIN`. An inaccessible vault is reported as
`404` / `NOT_FOUND`, just like a nonexistent vault.

```bash
SERVER_URL='https://backend.example.invalid'
API_KEY='noxsync_fictitious_example_key'
curl --fail --show-error -H "Authorization: Bearer $API_KEY" \
  "$SERVER_URL/v1/auth/check"
```

## Health and service information

### `GET /v1/health`

Public; returns `200` and `Content-Type: application/json`:

```json
{
  "status": "ready",
  "version": "1.2.3",
  "dataDirInitialized": true,
  "databasePath": "/data/nox-backend.db"
}
```

This preserves the existing readiness signal after startup has initialized the
data directories and opened the database. It does not introduce a new active
dependency probe. `databasePath` reflects the configured data directory.
The handler's legacy acceptance of other HTTP methods remains unchanged;
clients should use `GET`. Service discovery metadata belongs in `/v1/info`.

### `GET /v1/info`

Public; does not authenticate or access the storage layer. Returns `200` and
`Content-Type: application/json`, with these four initial fields:

```json
{
  "service": "nox-backend",
  "version": "1.2.3",
  "apiVersion": "v1",
  "capabilities": ["auth", "vaults", "files", "vault-status"]
}
```

`service`, `version` and `apiVersion` are strings; `capabilities` is an array of
strings. `vault-status` identifies `/v1/status`; it does not announce the
synchronization protocol as stable client API. Unsupported methods return
`405`, `Allow: GET`, and the standard JSON error with `code: "BAD_REQUEST"`.

Both health and info expose the same version resolved at startup:
non-empty `NOX_BACKEND_VERSION`, then the embedded build version, then `dev` for
unversioned local builds. Published images embed the exact Git tag of their
commit or `git-<full commit SHA>`; `latest` is only an image tag. See
[backend configuration](backend-configuration.md#local-docker-image-build).

## Current identity

### `GET /v1/auth/check`

Requires an API key. Returns `200`:

```json
{
  "ok": true,
  "user": "user@example.invalid",
  "role": "USER",
  "vault": "selected-in-settings"
}
```

`user` is the user's email, `role` is `USER` or `ADMIN`, and `vault` retains the
legacy fixed marker; it is not a vault ID. Select a vault using `/v1/vaults`.

## Vault lifecycle

All operations below require an API key. Query parameter `vaultId` is required
for deletion, restoration and purge. Successful lifecycle mutations return
`200` with `{"ok":true}`, except creation, which returns `201` with the vault.

| Method | Path | Input / behavior |
| --- | --- | --- |
| `GET` | `/v1/vaults` | List owned active and soft-deleted vaults. |
| `POST` | `/v1/vaults` | JSON body `{"name":"My Vault"}`; create an active vault. |
| `DELETE` | `/v1/vaults?vaultId=<ID>` | Soft-delete an active vault. |
| `POST` | `/v1/vaults/restore?vaultId=<ID>` | Restore a soft-deleted vault. |
| `POST` | `/v1/vaults/purge?vaultId=<ID>` | Permanently remove a soft-deleted vault. |

Example list response:

```json
{
  "vaults": [
    {
      "vaultId": "vault_example",
      "name": "My Vault",
      "revision": 0,
      "status": "ACTIVE",
      "updatedAt": "2026-10-03T10:00:00Z",
      "sizeBytes": 0
    }
  ]
}
```

Creation returns that vault object directly. `vaultId`, `name`, `status` and
`updatedAt` are strings; `revision` and `sizeBytes` are integers. `sizeBytes`
counts current non-deleted file bytes. `status` is an optional field in the
existing type and is populated as `ACTIVE` or `DELETED` in these responses.
`updatedAt` and optional `deletedAt` use UTC RFC 3339 timestamps.

`vaults` is always an array, including `[]` when there are no active vaults.
`deletedVaults` is omitted when empty; otherwise it contains the same vault
shape with `status: "DELETED"` and `deletedAt`. Each list is ordered by
`updatedAt` descending, then name ascending, without pagination.

Names are trimmed, must be non-empty and are limited to 160 bytes. Creation
uses the existing strict JSON decoder: unknown fields, malformed JSON and
multiple JSON values produce `400` / `BAD_REQUEST`.

A soft-deleted vault retains its data and may be restored, but is unavailable
for status, file reads and synchronization. Purge is irreversible and removes
unreferenced blobs while retaining blobs referenced by other vaults. Repeating
an operation when the vault is no longer in its required state returns
`404` / `NOT_FOUND`; these mutations are not idempotent success responses.

```bash
curl --fail --show-error -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' --data '{"name":"My Vault"}' \
  "$SERVER_URL/v1/vaults"
curl --fail --show-error --get -H "Authorization: Bearer $API_KEY" \
  --data-urlencode 'vaultId=vault_example' "$SERVER_URL/v1/status"
```

## Vault status

### `GET /v1/status?vaultId=<ID>`

Requires an API key and an owned active vault. Returns `200`:

```json
{
  "vaultId": "vault_example",
  "serverRevision": 0,
  "sync": {
    "state": "IDLE",
    "sessionId": "",
    "clientId": "",
    "clientName": "",
    "startedAt": ""
  }
}
```

`serverRevision` is an integer for the vault's committed revision. All fields
inside `sync` are strings. States are `IDLE`, `SYNCING`, `FAILED` and
`STALE_LOCK`; inactive session metadata is represented with empty strings.
`startedAt`, when populated, is a UTC RFC 3339 timestamp.

Status preserves the existing refresh of expired synchronization locks and
associated status events. It does not start or orchestrate a synchronization
session. File reads have their separate committed-state semantics described
in the read API reference.

## File listing and download

Both endpoints require an API key and an owned active vault:

| Method | Path | Success |
| --- | --- | --- |
| `GET` | `/v1/files?vaultId=<ID>` | `200` JSON list of current files and vault revision. |
| `GET` | `/v1/files/download?vaultId=<ID>&path=<PATH>` | `200` original file bytes. |

Download also accepts optional, independent `expectedHash` and
`expectedRevision` conditions. A version mismatch returns `409` /
`FILE_CHANGED`; a removed or renamed file returns `404` / `NOT_FOUND`.

The [read API reference](read-api.md) is authoritative for field shapes,
parameter validation, empty arrays, per-file revisions, binary downloads,
`X-NoX-Sync-*` headers, `Cache-Control: no-store`, error examples and consistency
limits. Existing downloads without conditions remain supported.

## Response conventions and stability

JSON responses use `Content-Type: application/json`. Downloads return
`application/octet-stream`. Errors use the existing shape:

```json
{
  "code": "BAD_REQUEST",
  "message": "vaultId is required."
}
```

Treat `code` as the machine-readable error identifier. `message` is informative
and may change. Do not assume every endpoint has the file API's cache policy.

| HTTP | Code | Meaning |
| --- | --- | --- |
| `400` | `BAD_REQUEST` | Invalid JSON, missing/invalid parameters or malformed download conditions. |
| `401` | `AUTH_REQUIRED` | Missing API key. |
| `401` | `AUTH_FAILED` | Invalid/rotated key or disabled user. |
| `404` | `NOT_FOUND` | Missing/inaccessible vault or file, or incompatible vault lifecycle state. |
| `405` | `BAD_REQUEST` | Unsupported method; the `Allow` header lists supported methods. |
| `409` | `FILE_CHANGED` | A valid download condition does not match the current file. |
| `500` | `SERVER_ERROR` | Internal backend failure. |

Existing validation order is preserved: `/v1/vaults` authenticates before
checking the method; the other method-restricted endpoints above check the
method first. Clients should send supported methods rather than depend on
uniform validation order. Health keeps its legacy method behavior.

Stable field names, types, error codes and semantics will be preserved within
this API. Compatible additions remain possible; clients should tolerate
unknown response properties and capability identifiers. JSON property order
is not part of the contract.

`/v1/sync/*` (events, begin, heartbeat, manifest, upload, commit and abort)
remains the specialized synchronization protocol, available unchanged to
synchronization clients. Dashboard and Google OAuth routes are outside this
stable client API. External clients do not need synchronization orchestration
to inspect identity, manage vaults or read files.
