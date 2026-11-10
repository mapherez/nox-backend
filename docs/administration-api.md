# Native administration API

These Backend-owned APIs expose native dashboard operations independently of
HTML and the future Portal. React uses these APIs; the legacy browser form,
NoX Login entry and dependent-product routes remain available. Existing `/v1/*`
and direct `/mcp` authentication and representations are unchanged.

These routes are not the Suite `/api/nox/*` service, pairing, registry or
issuance contracts. Those have separate [service protocol contracts](suite-trust.md).

## Authentication and request boundary

Every route below requires either:

- The existing active `nox_sync_session` browser cookie. Every mutation also
  requires `X-Nox-Request: 1`. Browser `Origin` must match the Backend public
  origin; cross-origin and same-site-but-different-origin requests are rejected.
  Configure `NOX_BACKEND_PUBLIC_URL` to the actual external browser origin.
- `Authorization: Bearer <personal-api-key>`, resolved to its current active
  account and role. An explicit invalid Authorization header never falls back
  to a valid cookie. The administration API does not accept `api_key` in a URL.

The API enables no CORS access. It does not create a new login scheme or forward
personal keys to other products. Google identity and account admission belong to
NoX Auth; Backend retains its app cookie and domain roles. Use `/auth/nox/start`
and `/auth/nox/callback`. `/auth/google/start` aliases the new entry for existing
integrations. See [NoX Login](nox-login.md).
`POST /api/session/logout` specifically requires browser-session authentication;
it removes that session and its cookie, without revoking the personal key.

JSON request bodies require `Content-Type: application/json` and one object,
limited to 64 KiB. Unknown fields, invalid enum values, trailing JSON and malformed
objects are rejected. Bodyless operations need no JSON body. No caller-supplied
user ID or role can override the authenticated actor. Responses use
`Cache-Control: no-store`, including errors and credential responses. Do not log
Authorization/cookies or API-key response bodies.

## Operations

| Method | Route | Input/result | Access |
| --- | --- | --- | --- |
| GET | `/api/account` | Current user's presentation fields | Current account |
| GET | `/api/account/api-key` | `{ "apiKey": "..." }` | Own key only |
| POST | `/api/account/api-key/rotate` | New `{ "apiKey": "..." }`; previous key immediately fails authentication | Own key only |
| POST | `/api/session/logout` | `{ "ok": true }`, expired session cookie | Browser session only |
| POST | `/api/session/logout-all` | `{ "ok": true }` only after Auth confirms Suite-session revocation | Browser session only |
| GET | `/api/admin/users` | Array of allowed users, without Google subjects or keys | Admin |
| POST | `/api/admin/users` | `{ "email": "person@example.com", "role": "USER" }`; creates/re-enables a local record and returns its presentation fields; does not change Auth admission | Admin |
| PUT | `/api/admin/users/{userId}/status` | `{ "status": "ACTIVE" }` or `DISABLED` | Admin |
| PUT | `/api/admin/users/{userId}/role` | `{ "role": "USER" }` or `ADMIN` | Admin |
| DELETE | `/api/admin/users/{userId}` | Deletes the account and its owned sync records | Admin |
| GET | `/api/vaults` | `{ "vaults": [...], "deletedVaults": [...] }` | Own vaults only |
| DELETE | `/api/vaults/{vaultId}` | Soft-delete | Owner |
| POST | `/api/vaults/{vaultId}/restore` | Restore a soft-deleted vault | Owner |
| DELETE | `/api/vaults/{vaultId}/purge` | Permanently purge a deleted vault | Owner |
| GET | `/api/vaults/{vaultId}/download` | ZIP attachment; failures use JSON | Owner |
| GET | `/api/admin/connections` | Foundation-service connection intent and configuration revisions | Admin |
| PUT | `/api/admin/connections/{service}` | Save `{ "origin": "https://yard.example.com" }`, with a creation/update precondition | Admin |
| DELETE | `/api/admin/connections/{service}` | Remove saved connection intent, with its revision precondition | Admin |
| POST | `/api/admin/suite/backend/publish` | No body or query; publish/reconcile Backend's own Suite association when configured | Admin |

Successful mutations return HTTP 200 with `{ "ok": true }` except operations
that explicitly return a user, key or connection. User roles and statuses are
normalized to uppercase; omitted role on add defaults to `USER` for compatibility
with the legacy form. Unsupported explicit values are rejected.

### Admin Managers and account management

Admitted ADMIN accounts signed in through NoX Login have **Admin Manager**
authority. Existing USER roles are preserved. Managers can manage other admins;
nobody may delete, disable or change their own role. The central account list is
configured only in Auth. Backend user administration controls local roles/status
and data, and never adds an email to that central list. Newly admitted accounts
receive an ADMIN record. Existing identities, keys and ownership are preserved.

User representations include `isAdminManager` and actor-specific
`permissions.canDelete`, `permissions.canChangeRole` and
`permissions.canChangeStatus`. React uses these to show **Make member**, status
and delete controls for manageable admins. These fields are read-only projections;
the API always enforces the same authority independently. If an older Backend
does not return these capabilities, the independent frontend stays usable and
disables account changes with an explicit Backend-update message.
Target and self-account restrictions are also part of the SQL write, so a concurrent promotion
cannot bypass the current manager policy. Deleting an admin removes its account,
keys, sessions and owned sync records with the existing deletion semantics.

An admin role grants no access to another account's vaults. Foreign and missing
vaults both return 404. Vault IDs must be URL-encoded as a path segment. Vault
`revision` and `sizeBytes` are decimal strings so browsers preserve the full int64
range. The existing client API remains the creation/synchronization API; vault
creation uses the client API rather than a separate administration route.

`GET /api/dashboard` remains the native browser bootstrap aggregate. It uses only
session authentication, returns an anonymous login/configuration view otherwise,
and includes users and connection configuration only for admins. Its public
login/configuration projection does not grant administration rights.

## Initial Yard/Gateway configuration

Here `service` is the foundation role `yard` or `gateway`, not a list of supported
Suite products. Product enrollment is generic under the registry
contract. Each entry contains `service`, `origin`, `revision`, `updatedAt` and:

- `not_configured`: no saved address, with empty origin/revision/timestamp.
- `not_paired`: address saved; no trust or connection success is implied.
- `pairing_pending`: awaiting the service's completed/persisted provisioning.
- `paired`: technical association acknowledged; registry membership remains pending.
- `registered`: current technical association matches accepted membership; availability remains separate.
- `recovery_required`: association retained but no usable credential generation.

Origin validation uses SDK `NormalizeSuiteOrigin`: an HTTP or HTTPS IP/hostname
with an optional port, with a root slash normalized. Credentials, queries,
fragments, `/apps/...` and other base paths are rejected. Private HTTP origins
are supported without certificates.

Saving an address stores native web configuration only. **Verify** contacts the
product's same-host Unix socket and checks its actual declaration; **Connect**
performs pairing, credential delivery, acknowledgment and registration. The web
URL is not fetched to establish same-host presence. The browser receives no
pairing secret or service credential. See [automatic local setup](suite-local.md)
and [service trust](suite-trust.md).

Create with `If-None-Match: *`. Change/remove with `If-Match: "<revision>"` using
the current entry's opaque revision. PUT also returns that quoted value in ETag.
Missing/invalid preconditions return 428; stale edits, duplicate creation and
stale/deleted revisions return 412. Refresh and review before retrying; never
silently overwrite another administrator's change. A successful save generates
a fresh revision; removing and recreating configuration never recycles one.
Removing the saved address does not uninstall a product or revoke a paired
association. Use the [trust APIs](suite-trust.md) for credential/service revocation;
registry membership is managed separately by the [registry APIs](suite-registry.md).

Example creation from the same-origin browser session:

```js
const response = await fetch('/api/admin/connections/yard', {
  method: 'PUT',
  credentials: 'same-origin',
  headers: {
    'Content-Type': 'application/json',
    'X-Nox-Request': '1',
    'If-None-Match': '*',
  },
  body: JSON.stringify({ origin: 'http://yard.home.arpa:8095' }),
});
// Check response.ok; 412 requires a fresh read and administrator review.
```

## Errors and frontend behavior

Errors are JSON `{ "code": "...", "message": "..." }`, never login redirects:

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `BAD_REQUEST` | Invalid fields or protected admin operation |
| 401 | `AUTH_REQUIRED` / `AUTH_FAILED` | Missing, expired, disabled or invalid authentication |
| 403 | `FORBIDDEN` | Current role or browser-origin/request protection rejects access |
| 404 | `NOT_FOUND` | Unknown resource/route, or vault not owned by this account |
| 405 | `METHOD_NOT_ALLOWED` | Unsupported method; `Allow` lists accepted methods |
| 412 | `PRECONDITION_FAILED` | Connection configuration revision changed |
| 413 | `BODY_TOO_LARGE` | Administration body exceeds 64 KiB |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | JSON mutation sent with another Content-Type |
| 428 | `PRECONDITION_REQUIRED` | Connection edit lacks the required condition |
| 500 | `SERVER_ERROR` | Internal operation failure; no database internals returned |

The frontend submits JSON with the current session, disables forms during a
mutation, shows API errors alongside the form, and refreshes the authenticated
view after success. It never automatically replays a mutation. An unconfirmed
network outcome tells the user to refresh before retrying. If a confirmed write
succeeds but refresh fails, further changes are disabled until reload. Existing
key-rotation, destructive-action and permanent-delete confirmations remain.
The ZIP endpoint intentionally returns binary content on success; access and
metadata failures before streaming return JSON.

## Persistence and delivery

Migration 4 adds only `foundation_connections` to the existing SQLite database.
Startup first validates the installed multi-user/multi-vault schema/history
(1–3), then applies this additive migration. It does not rerun historical reset 3
or regenerate keys, sessions, vaults, revisions or data directories. Restart
retains connection intent/revisions. Unsupported historical or tampered schemas
continue to fail before modification.

Back up all of `/data` before upgrading and validate a copy as described in the
[storage guide](backend-separation.md). Older binaries reject the new migration
history: rollback across this schema addition requires the retained pre-upgrade
data backup and the matching old images/topology; it must not run an old binary
against the upgraded database or delete migration records to bypass validation.
A frontend-only rollback to a compatible UI does not roll back the database.

CI and release workflows run the Go and frontend checks and disposable
split-image/browser validation. Related contracts cover [service trust](suite-trust.md),
[registry/events](suite-registry.md), [delegation](suite-authorization.md),
[Suite MCP](suite-product.md) and [native Suite administration](suite-administration.md).
See [delivery](suite-delivery.md) for installed verification.

## Backend tool publication

With `NOX_BACKEND_SUITE_MCP_ENABLED=true`, the authenticated administrator dashboard
calls `POST /api/admin/suite/backend/publish` automatically. It uses the existing
session and CSRF protection; explicit callers can use an active admin personal key.
The route accepts no body or query and returns `{ "enabled": true }` on success
when Suite MCP is enabled. It never returns service credentials.

Publication uses the normal pairing/proof/ACK/registration operations and private
storage. Repeated unchanged publication keeps the association/revision. Recovery
after restart remains bound to the originating administrator's active role and
Auth admission. Removed/revoked associations are not silently recreated. See
[Backend product setup](suite-product.md#trust-and-enrollment).

## Suite administration snapshot

`GET /api/admin/suite` is a native admin-only, JSON/no-store diagnostic projection.
It accepts a current admin personal key or browser session. Storage rechecks the
role and supplies consistent safe pairing/service/credential/deployment/key and
registry/observation state. It exposes no raw secret or verifier. It is available
on native HTTP; its configuration describes the same-host local channel.
Non-secret administration uses `/api/admin/suite/operations/*` with the existing
conditional/idempotent rules. Pairing and credential delivery use the local
participant protocol, not the browser adapter. See the
[native integration workflow](suite-administration.md).

Failure of Suite connection/status reads no longer fails the complete
`/api/dashboard` response. Admins receive a safe `integrationError` while their
native account/key/vault controls continue to operate. The independent Suite pane
fails closed until its status can be refreshed.
