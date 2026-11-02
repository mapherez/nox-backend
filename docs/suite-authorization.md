# Suite authorization issuance

Backend point 6 implements the [approved Gateway/integration contract](suite-migration.md#approved-references-and-progression)
using NoX MCP SDK 0.7.2 and wire version 1.0. It uses the persistent
[issuer/signing keys and service trust](suite-trust.md) and
[registry/event feed](suite-registry.md). It is generic to every conforming product.
It does not implement Gateway, Portal or CLI. Backend's own protected
[Suite MCP adapter](suite-product.md) is implemented in point 7. The existing native frontend, Google login, personal API keys,
client APIs and independently authenticated direct `/mcp` remain operational.

## Protected exchange routes

| Operation | Route | Required caller grant |
| --- | --- | --- |
| User delegation | `POST /api/nox/authorizations` | Current Gateway service with `authorization.delegate` |
| Service readiness probe | `POST /api/nox/service-authorizations` | Current Gateway service with `mcp.probe` |

Both require the [same-host local channel](suite-local.md) and
`Authorization: Bearer <gateway-service-credential>`. A personal key, Google cookie,
installer/product credential or unacknowledged provisioned credential cannot act as
the Gateway. Caller grant/head/credential validity is checked before parsing the
exchange and checked again in the issuance transaction. Query parameters are rejected;
credentials must not be placed in URLs. Bodies use strict bounded SDK JSON parsing,
including duplicate-key, version and required-feature checks.

These POSTs read current authority and issue a fresh bounded grant. They are not
business mutations or replayable management transactions: `Idempotency-Key` is not
required, no original grant is replayed, and each successful exchange receives a
new `jti`. A retry checks current authority again. Responses use JSON/no-store and
never return the supplied personal key or Gateway service credential. Tokens are
not stored in idempotency rows, events, ordinary caches, or new secret-store files.
The existing legacy personal-key storage is not migrated by this work.

User request (SDK `SuiteAuthorizationRequest`):

```json
{
  "contractVersion": "1.0",
  "apiKey": "<personal-api-key>",
  "productId": "<actual-registered-product>",
  "registrationRevision": "<current-registration-revision>"
}
```

The personal key is present only in the protected request body to Backend. Backend
resolves the exact key verifier, active allowlisted account, current role, persistent
personal `keyId` and `authRevision` locally. It does not contact Google, recreate a
personal key, infer a domain account or accept caller-supplied identity/capabilities.
Prepared accounts/keys therefore work without Internet access after restart.

| Existing role | Granted scopes |
| --- | --- |
| `USER` | `nox:inspect` |
| `ADMIN` | `nox:inspect`, `nox:write`, `nox:sensitive-read` |

Admin Managers have the existing `ADMIN` role for this mapping; their additional
account-management protection remains server-derived. No role enum, per-user/tool
matrix, domain ownership exception or product-specific permission rule is added.
Optional request assertions such as scopes, role, audience, user ID or lifetime are
inert and never override Backend authority.

Probe request (SDK `SuiteProbeRequest`):

```json
{
  "contractVersion": "1.0",
  "productId": "<actual-registered-product>",
  "registrationRevision": "<current-registration-revision>"
}
```

A probe needs no personal key or user session. It has only `nox:mcp-probe`, a service
subject and `purpose: mcp-probe`; user actor/key/auth-revision claims are absent.

## Current target and signed profiles

The target must exist at the supplied registration revision, advertise `suiteMcp`,
and still match its active acknowledged trusted association, origin and management
binding. Revoked, removed, replaced, unsupported or known-incompatible targets do
not receive new grants. The audience is derived exclusively from the accepted
origin and Suite MCP path using the SDK's canonical endpoint resolver. Ordinary
network unavailability alone does not remove membership or change central authority;
issuance does not refetch the product on every exchange.

A pinned declaration fetch that establishes unsupported/malformed integration, or a
different product identity during an authorized update, preserves the last accepted
record but blocks new user/probe issuance for that exact registration/association.
The block and its authorization invalidation event commit together and survive
restart. A stale verification result cannot block a newer association. A fresh
successful verified update clears the block, including when the material accepted
record is identical. Mere replay of an older committed registration does not clear
it. Transient network failures are not recorded as incompatible declarations.

Backend signs compact JWS with ES256 and its current persistent signing key. There
is no algorithm negotiation or key URL in the token. User headers use
`typ: nox-delegation+jwt`; probe headers use `typ: nox-service+jwt`, each with trusted
`kid`. `kid` identifies the signing key; the user's `nox.keyId` independently
identifies the personal key generation. They are not interchangeable.

User claims contain the Backend issuer, exact product audience, Backend user subject,
Gateway actor, current service-credential revision, personal-key/auth revisions,
product/registration/association bindings, granted scopes and unique grant ID.
Probe claims contain the service subject, target and credential bindings and probe
purpose/scope. The response `context` agrees with the signed claims; `expiresAt`
agrees exactly with `exp`. Request-local values prevent concurrent users sharing
an identity or receiving each other's capabilities.

Tokens expire at most 300 seconds after issuance, bounded earlier by the authenticating
service credential's validity (including a credential-rotation overlap). Existing
personal keys have no configured expiry; their revocation/status/rotation is checked
on each exchange. The shared SDK refuses admission at `exp - 5 seconds`; there is
no symmetric grace period accepting expired authority. Significant clock rollback
fails issuance closed. A revoked/missing active signer returns 503 instead of
silently generating another issuer/key or using a retired private signer.

These are reusable grants for a product, not single-use approvals or tool/argument
hashes. They do not execute or authorize a business mutation themselves. Product
adapters must enforce classification, scopes, ownership, invalidation state and
execution deadlines for each operation. A probe is limited to `initialize`,
`notifications/initialized`, `ping` and supported `server/discover`; it cannot list
or call tools or retrieve a user's catalog. Every message in a batch needs checking.
The SDK supports those admission checks; [Backend's own Suite endpoint and tool
classification](suite-product.md) implement them in point 7.

## Invalidation and long sessions

Migration 7 adds separate authorization/key-generation metadata while keeping the
existing account and API-key values. Native account role/status changes, deletion,
and personal-key rotation/revocation write `authorization.invalidated` atomically
with their authority change. Triggers cover every existing native/API/MCP storage
entry point and reject the authority mutation if event writing fails. No effective
role/status change means no new authorization revision or invalidation. Normal
Google login/profile updates and restart do not rotate authorization metadata.
The installed-schema guard verifies the required trigger definitions.

Events expose only affected opaque user/key IDs. User invalidations target all that
user's grants; rotation also names the retired personal `keyId`. Existing service,
signing-key, registry and association invalidations retain their event profiles.
The existing event retention/housekeeping mechanism remains responsible for cleanup;
no additional destructive retention trigger was introduced. Native commits are
visible to service subscriptions through the feed's bounded periodic check.

Gateway consumers must apply those events to entries, authorized views and pending
renewals. A renewal already in flight cannot repopulate an invalidated cache. New
permissions require a new authenticated Backend response, not a label in an event.
Use the released SDK's partitioned cache and invalidation barrier; the cache and
Gateway host itself are not implemented in this repository.

A 1–3-hour work session does not force repeated Google login. Future consumers
reuse the grant for its original bounded lifetime and renew on subsequent use when
needed. Waiting at a form is not an authorization poll. Backend does not start a
per-user renewal timer. A valid admitted operation follows its execution deadline;
its completion is not cut off by token expiry during the operation.

If an invalidation is delayed/lost, residual authority lasts only until the original
expiry, at most five minutes. Explicit denial cannot fall back to an older positive
grant. Backend outage permits only matching still-valid existing grants under the
contract; it cannot create new ones, extend expiry or renew locally. Cursor expiry
requires complete reconciliation and invalidation of potentially missed state.
Normal restart preserves issuer, keys, auth/key revisions, registry and event log.

## Errors and deployment

Responses retain the Suite management error shape. Missing/invalid credentials or
an inactive personal account are 401; missing service grants/role are 403; absent
registration is 404; changed registration/association is 409; absent Suite capability
or blocked incompatibility is 422; unavailable signer/storage/clock is 503. No API
failure redirects to a dashboard or exposes an input key/token. Issuance grants are
not accepted as legacy client API keys.

Migration 7 is additive to the same SQLite database and `/data`; keep the complete
backup including `/data/secrets/suite`. It adds user authority revisions, opaque
personal-key generations, declaration admission state and native invalidation
triggers. Existing users, native sessions, personal keys, vaults, blobs, trust and
registrations remain. No new environment variable, container or public port is
required. Native HTTP use remains supported; Suite issuance uses the local participant channel. Rollback
requires the old binary's matching complete data backup, as with prior migrations.
