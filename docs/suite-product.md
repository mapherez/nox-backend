# Backend as a NoX Suite product

Backend provides a protected Suite product adapter using the published
NoX MCP Go SDK `v0.7.3` and Suite contract `1.0`. It shares the ten existing MCP
operations and storage with [direct MCP](mcp.md) and the Stable Client API.
Gateway routes this adapter to clients; the CLI renders its human-menu definitions.
Those applications and the future Portal have separate repositories.
The native frontend remains part of this Backend project.

## Paths and independent configuration

| Path | Policy |
| --- | --- |
| `GET /nox/integration` | Public, SDK-validated product declaration; no credentials, tool definitions or automatic membership |
| `POST /mcp/suite` | Opt-in, same-host Unix socket, current Backend-issued product-bound user delegation or service probe |
| `POST /mcp` | Existing direct personal-key/public-tool policy, enabled by default |

The direct/native APIs keep their TCP listener and frontend routing. Protected Suite MCP is available only on the local Unix listener; the native listener/frontend refuse network access to it. These remain distinct authentication adapters over the same domain operations. See [same-host transport](suite-local.md).

| Environment variable | Default | Effect |
| --- | --- | --- |
| `NOX_BACKEND_DIRECT_MCP_ENABLED` | `true` | Mount the existing direct `/mcp` endpoint |
| `NOX_BACKEND_SUITE_MCP_ENABLED` | `false` | Mount `/mcp/suite` and advertise `capabilities.suiteMcp.path` |

Use literal `true` or `false`; invalid values stop startup. The existing
`NOX_SYNC_*` aliases work, with canonical variables taking precedence. Both
Compose files pass these options through. Existing installations need no env
change for their native frontend, APIs or direct MCP.

When Suite is disabled the declaration has `capabilities: {}` and the Backend
Suite route is absent. When enabled it advertises:

```json
{
  "contractVersion": "1.0",
  "productId": "nox-backend",
  "productVersion": "<resolved-backend-version>",
  "displayName": "NoX Backend",
  "capabilities": {"suiteMcp": {"path": "/mcp/suite"}}
}
```

The actual response uses the runtime version from the same source as `/v1/info`.
It has `Cache-Control: no-store` and is available without Google login. Declaration
availability means integration capability, not successful pairing or registration.

## Trust and enrollment

Before using protected Suite MCP:

1. Deploy the image/Compose with its local Suite volume. Native web/OAuth URLs keep their existing function and do not establish service locality.
2. Enable `NOX_BACKEND_SUITE_MCP_ENABLED=true` when the Backend product is to expose tools through Suite.
3. Open the native Backend dashboard as the existing NoX Auth administrator. With Suite MCP enabled, the dashboard automatically sends an authenticated, same-origin `POST /api/admin/suite/backend/publish`. Backend performs its own pairing, HMAC proof, durable provisioning, ACK and verified registration through the existing operations, using its actual declaration and logical `http://nox-backend` origin. No pairing JSON, service credential or certificate is copied by the user or sent to the browser. The declaration alone does not create membership.
4. An acknowledged Gateway service with the appropriate grant requests a
   [user delegation or service probe](suite-authorization.md) for the current
   `nox-backend` registration revision. Its audience is exactly the accepted
   Backend origin plus `/mcp/suite`.

The initial publication requires that existing administrator session; it is not
an anonymous startup privilege. Reopening the dashboard retains the same association
and unchanged registration revision. After a restart, the existing reconciler may
refresh publication using the administrator recorded on the acknowledged pairing,
only while that account remains active, administrative and admitted by Auth. A
crash after ACK but before registration is recovered from that same persisted state.
Interrupted pre-ACK attempts from this workflow are canceled before replacement;
unrelated administrative pairings are not canceled. Explicitly removed/revoked
associations or credentials are never recreated by this automation.

Disabling Suite MCP withdraws its advertised capability on reconciliation while
retaining the association and leaving direct MCP independent. Re-enabling the flag
can republish that same active association. Missing local transport or publication
failures are shown in the dashboard with an explicit retry action. The new Backend
and frontend images must both be installed for the initial dashboard trigger.
No database reset, new environment variables or manual registry calls are needed.

Send only the resulting token as `Authorization: Bearer <suite-access-token>`.
Native personal keys, service credentials and browser cookies cannot authenticate
this endpoint. Query parameters are rejected; no query credential fallback exists.
The existing direct endpoint retains its own legacy policy.

Backend validates its own authority through a local storage adapter. It does not
call Google, an external product, or its own HTTP authorization API per tool.
Signature, issuer, canonical resource, profile, current Gateway/head/credential
revision, own registration/association/admission, signing key, active user,
personal key ID, authorization revision and role-derived scopes are checked from
current local state. Unknown/revoked state fails closed. Invalid authority returns
HTTP 401; local authority/configuration failure returns a safe 503. TCP access or forged transport claims return 403, including through the frontend proxy.

Verification snapshots and native user projections belong to the request. They
are never a shared mutable current-user field. The HTTP factory uses `NewSuite`,
including its protocol admission, filtered discovery and policy metadata.

## Explicit tool policy and ownership

Every canonical Suite tool definition retains its existing `_meta.cli` and the
SDK's `_meta["nox/authorization"]` policy. Standard `tools/list` excludes only
`_meta.cli` and preserves the authorization policy; `nox/tools/definitions`
retains both for NoX clients. Unclassified future tools stay absent until an
explicit policy is supplied. The private discovery view is partitioned by verified
identity/authorization context; renewing an equivalent grant does not make token
bytes or expiry part of the catalog view.

The same private discovery definitions include the [human menu descriptors](mcp.md#human-menus-through-nox-discovery): navigation labels/order, existing vault-list selectors
and local confirmations. They do not alter tool arguments, results or Suite scopes.
The standard MCP catalog continues to omit CLI metadata and descriptors.

| Tools | Required scopes |
| --- | --- |
| `backend_info`, `backend_whoami`, `backend_vault_list`, `backend_vault_status`, `backend_file_list` | `nox:inspect` |
| `backend_health` | `nox:inspect`, `nox:sensitive-read` |
| `backend_vault_create`, `backend_vault_delete`, `backend_vault_restore`, `backend_vault_purge` | `nox:inspect`, `nox:write` |

Health includes the existing internal database path, so Suite treats it as
sensitive diagnostics. Direct health/info behavior remains unchanged. USER has
five visible inspect tools; ADMIN has all ten. A guessed filtered tool name is
rejected by the SDK with MCP `FORBIDDEN`; hiding it in discovery is not the only
permission check. Existing domain errors and successful structured results remain
those of the shared operations.

All vault/file operations still require the delegated user's own vault. ADMIN and
Admin Manager confer no access to another user's data. Missing and inaccessible
vaults both produce `NOT_FOUND`. Suite write capability does not change the direct
personal-key API's existing ability to manage the key owner's vaults.

A service probe can initialize and ping, and use `server/discover` if supported by
the runtime. It cannot list/discover tool definitions or execute any business
operation. Every message in a batch is checked before dispatch. There is no user
projection or tool authority for the probe profile.

## Deadlines, capacity and writes

The SDK enforces canonical `Nox-Request-Id` (UUID), `Nox-Deadline-At` (UTC) and
positive canonical integer `Nox-Timeout-Ms` on `tools/call`. Missing/invalid controls
are rejected; expired budgets do not execute a mutation. The effective deadline
shrinks with elapsed work and the clock-safety margin. The local request bound is
20 seconds, the SDK input/result bound is 2 MiB, and execution capacity is 32 across
all users, requests and request-local runtime factories. HTTP admission is also
bounded at 32 requests. Overload rejects dispatch instead of accumulating a queue.
An executor that outlives cancellation retains its execution slot until actual exit.

Shared vault mutations revalidate current authority inside the same SQLite write
transaction as the domain write, preserving user ownership. Cancellation is checked
before the mutation and before commit. This closes a role/key/trust revocation race
between HTTP admission and a delayed write. Blob cleanup retains the existing
unreferenced-blob semantics. A grant expiring after valid admission does not shorten
the accepted operation's own deadline; current revocation checks still apply.
Timeout/cancellation of writes retains the SDK's unknown-outcome/no-automatic-retry
semantics. Read current state before retrying an uncertain mutation.

A five-minute delegation reuse bound is not a working-session timeout. A user can
work for hours; Gateway renews expired grants when a new operation needs
them. The product adapter does not prompt for Google login every five minutes.

## Validation and delivery boundary

Tests enroll the disposable Backend through the local service management
flow and invoke it through the official MCP Go client. They cover role-filtered
catalogs, user isolation, vault lifecycle/ownership, profile separation, revoked
accounts/keys/Gateway/signing keys/membership, incompatible admission, payload and
request controls, concurrent identities, and retained capacity after timeout.
A storage test covers expiry after admission, current revocation before a write,
and clock rollback. Existing CI/release `go test -race ./...` includes these tests.

The required image-level local-channel check validates the declaration, Suite token
boundary and rejection of forged HTTP transport claims on `/mcp/suite`; its
frontend-only mode covers the new proxy guard independently. Native frontend,
API/direct MCP, sync streaming and frontend replacement remain covered by the
existing Chromium/split-deployment checks. No database migration is added here.
[Native integration administration](suite-administration.md) provides setup and recovery.
See the [delivery guide](suite-delivery.md) for publication/installed validation and
the boundary with subsequent Gateway/product work.
