# Backend as a NoX Suite product

Backend point 7 implements the protected product adapter using the published
NoX MCP Go SDK `v0.7.0` and Suite contract `1.0`. It shares the ten existing MCP
operations and storage with [direct MCP](mcp.md) and the Stable Client API.
Gateway, CLI and Portal are separate products; this work does not implement them.
The native frontend remains part of this Backend project.

## Paths and independent configuration

| Path | Policy |
| --- | --- |
| `GET /nox/integration` | Public, SDK-validated product declaration; no credentials, tool definitions or automatic membership |
| `POST /mcp/suite` | Opt-in, trusted HTTPS, current Backend-issued product-bound user delegation or service probe |
| `POST /mcp` | Existing direct personal-key/public-tool policy, enabled by default |

Both MCP paths run on the same listener/port as the Backend APIs and are forwarded
by the separate frontend. They are different authentication adapters over the same
operations. No additional listener, Gateway implementation or public Internet
exposure is required. Suite origins use a hostname or IP plus optional port;
reverse-proxy application prefixes are outside the approved Suite contract.

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

1. Configure the actual HTTPS `NOX_BACKEND_PUBLIC_URL` and
   [TLS/trusted-proxy policy](suite-trust.md). Internal CA certificates and private
   LAN/Twingate origins are suitable; HTTP remains rejected for Suite authority.
2. Enable `NOX_BACKEND_SUITE_MCP_ENABLED=true` and deploy both updated images.
3. Enroll `nox-backend` as an actual product through the existing authorized
   [pairing/ACK](suite-trust.md) and [verified registry](suite-registry.md) APIs,
   using its own real declaration, HTTPS origin and TLS key proof. The declaration
   alone never creates trust or membership. The native setup controls are point 8.
4. An acknowledged Gateway service with the appropriate grant requests a
   [user delegation or service probe](suite-authorization.md) for the current
   `nox-backend` registration revision. Its audience is exactly the accepted
   Backend origin plus `/mcp/suite`.

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
HTTP 401; local authority/configuration failure returns a safe 503. Plain HTTP or
forged transport claims return 403, including through the frontend proxy.

Verification snapshots and native user projections belong to the request. They
are never a shared mutable current-user field. The HTTP factory uses `NewSuite`,
including its protocol admission, filtered discovery and policy metadata.

## Explicit tool policy and ownership

Every exposed Suite tool has its existing `_meta.cli` and the SDK's
`_meta["nox/authorization"]` policy. Unclassified future tools stay absent until an
explicit policy is supplied. The private discovery view is partitioned by verified
identity/authorization context; renewing an equivalent grant does not make token
bytes or expiry part of the catalog view.

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
work for hours; the future Gateway renews expired grants when a new operation needs
them. The product adapter does not prompt for Google login every five minutes.

## Validation and delivery boundary

Tests enroll the actual disposable HTTPS Backend through the public management
flow and invoke it through the official MCP Go client. They cover role-filtered
catalogs, user isolation, vault lifecycle/ownership, profile separation, revoked
accounts/keys/Gateway/signing keys/membership, incompatible admission, payload and
request controls, concurrent identities, and retained capacity after timeout.
A storage test covers expiry after admission, current revocation before a write,
and clock rollback. Existing CI/release `go test -race ./...` includes these tests.

The required image-level HTTPS check now validates the declaration, Suite token
boundary and rejection of forged HTTP transport claims on `/mcp/suite`; its
frontend-only mode covers the new proxy guard independently. Native frontend,
API/direct MCP, sync streaming and frontend replacement remain covered by the
existing Chromium/split-deployment checks. No database migration is added here.
Native integration setup/administration and final complete delivery are point 8.
