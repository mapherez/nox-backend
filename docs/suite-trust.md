# Suite authority, pairing and service trust

Backend implements service trust with published NoX MCP `0.7.3`, wire contract
`1.0`, over the [same-host channel](suite-local.md). These APIs are HTTP
management operations, not new MCP methods. They work for any conforming product;
`gateway`, `installer` and `product` are technical purposes, not a product catalog.

Backend now persists its issuer, ES256 signing keys, verified service associations,
credential generations, pairing recovery and conditional/idempotent outcomes.
Related contracts cover [registry/events](suite-registry.md),
[user/probe delegation](suite-authorization.md), [Backend Suite MCP](suite-product.md)
and [native setup](suite-administration.md). Use [delivery](suite-delivery.md)
for updates and installed validation.
A paired service is not a registered product and has no integrated execution rights.
Existing direct `/mcp`, `/v1/*`, NoX Login and native account/vault administration
retain their own authentication and behavior.

## Transport and target policy

The [same-host channel](suite-local.md) carries service calls over Unix sockets in a shared local Docker volume. Suite services on another machine cannot join through LAN/private IPs, Internet connections or HTTPS. The native TCP listener refuses `/api/nox/*` and `/mcp/suite`; current human administrator operations use `/api/admin/*` with existing role/session/key/CSRF checks. All service credentials, signatures, capabilities and domain checks remain required.

The supplied images and Compose prepare the local mount and permissions. Backend/Gateway need no Docker socket or certificate authority. Canonical logical origins are mapped to product sockets with no DNS, network proxy, redirects or TCP fallback. Native web URLs remain separate. See the local-channel guide for naming and deployment migration.

### TLS deployment choices

TLS remains an independent option for the existing native web/client listener or reverse proxy. Existing certificate/key options and packaged TLS healthcheck support remain available. They are not prerequisites for same-host Suite pairing, and trusted TLS/proxy headers never grant a local service connection.

## Caller authority and service grants

Administrator calls use an existing active NoX Login browser session or personal
Bearer key. Cookie mutations additionally require `X-Nox-Request: 1` and a matching
browser origin, just like native administration. Authentication and authorization
are rechecked against Backend-local state for each management request and again
inside mutation transactions. No repeated Google login is introduced.

Service tokens are 256-bit random credentials, formatted separately from personal
keys. SQLite stores their SHA-256 verifiers. They authenticate only Suite service
operations, never a Google user, native administration or the legacy client API.
Pending credentials can acknowledge their own provisioning and inspect their own
metadata; they do not obtain ordinary service grants until acknowledged.

| Purpose approved by administrator | Assigned grants |
| --- | --- |
| `gateway` | `registry.read`, `authorization.delegate`, `events.read`, `mcp.report`, `mcp.probe` |
| `installer` | `registry.read`, `registry.manage-associated`, `events.read`, `lifecycle.report`, `pairing.manage-associated` |
| `product` | `events.read` |

A request's optional `grants` or identity claims cannot override these assignments.
A Gateway cannot enroll services. Administrative pairing delegates management of the installer’s own local
deployments. It can request only `product` pairing for its exact deployment,
product ID and canonical local origin. It cannot claim another installer’s
product, a standalone/foundation association, or its own product identity. Its
`managedBy.productId` must identify that authenticated installer. Changing or
withdrawing that authorization prevents pending proof completion/acknowledgment.
Read access to service metadata is admin-wide or restricted to the service itself.

## Conditional, idempotent management

Every mutation requires a printable `Idempotency-Key` of 1–128 characters. Scope is
the authenticated account/service and operation. The canonical request digest binds
method, target, body and conditional headers. A matching authorized retry returns
the original outcome before testing a now-stale revision; changed contents conflict.
Current authorization is required even for a replay. Outcomes survive restart for
at least 24 hours. After that window, read/reconcile before issuing fresh intent.

Secret-bearing outcomes are stored as encrypted secret-store references, not raw
idempotency JSON. Recovery closes on acknowledgment or expiry; a retry cannot
recover a consumed/expired secret, extend its lifetime or mint another credential.
Such a request returns `PAIRING_CONFLICT` and requires reconciliation. Non-secret
acknowledgment outcomes remain replayable under current caller authorization.

Use `If-None-Match: *` for initial pairing/deployment authorization and strong
`If-Match: "<revision>"` for replacement, cancellation, rotation or revocation.
Missing conditions return 428; stale conditions return 412. Pairing completion is
conditionally bound by its nonce/proof/expected association; acknowledgment is
bound to the newly provisioned credential rather than an administrator assertion.

### Routes

| Operation | Method and route | Access/precondition |
| --- | --- | --- |
| Read issuer/public keys | `GET /api/nox/keys` | Local Unix listener; public JSON and key-set ETag |
| Prepare signing key | `POST /api/nox/keys` | Admin; If-Match key-set revision; returns prepared key ID and new revision |
| Activate prepared key | `POST /api/nox/keys/{keyId}/activate` | Admin; If-Match current key-set revision |
| Revoke signing key | `DELETE /api/nox/keys/{keyId}` | Admin; If-Match key-set revision |
| Create intent | `POST /api/nox/pairings` | Admin or scoped installer; initial/replacement association condition |
| Read safe pairing state | `GET /api/nox/pairings/{pairingId}` | Admin or its authorized creating installer; no secret |
| Complete/recover proof | `POST /api/nox/pairings/{pairingId}/complete` | Bound HMAC proof over the local channel |
| Acknowledge persisted provisioning | `POST /api/nox/pairings/{pairingId}/ack` | Newly issued service credential |
| Cancel pending intent | `DELETE /api/nox/pairings/{pairingId}` | Authorized admin/installer; If-Match pairing revision |
| Read services / one service | `GET /api/nox/services`, `GET /api/nox/services/{serviceId}` | Admin list; admin or service for one record |
| Revoke full association/service | `DELETE /api/nox/services/{serviceId}` | Admin/current service; If-Match association revision; all generations revoked |
| Rotate service credential | `POST /api/nox/services/{serviceId}/credentials` | Admin/current service; If-Match credential revision |
| Acknowledge rotation | `POST /api/nox/services/{serviceId}/credentials/{credentialId}/ack` | Replacement credential only |
| Revoke credential generation | `DELETE /api/nox/services/{serviceId}/credentials/{credentialId}` | Admin/current service; If-Match target credential revision |
| Read installer deployment grants | `GET /api/nox/services/{serviceId}/deployments` | Admin or that scoped installer |
| Authorize/change/remove deployment | `PUT` / `DELETE /api/nox/services/{serviceId}/deployments/{deploymentId}` | Admin or that current scoped installer; initial/current deployment revision |

SDK-defined pairing/proof/provisioning/rotation payloads retain their published
schemas. The key-management, service-state, deployment-authorization and rotation
acknowledgment routes are Backend adapters; they do not introduce MCP extensions.
Public key-set JSON contains `contractVersion`, `issuer`, `revision`, and `keys`.
Service reads expose `revision` for the association and nested `service` containing
the current credential's ID/revision/grants, without its bearer secret.

Deployment authorization input is `{ "productId": "<actual-product-id>",
"baseUrl": "http://example-product" }`. It authorizes that installer/deployment;
it is not registry membership, a Docker schema or permission to enroll other targets.
The current enrolled installer can create and retire its own associations through
its service credential on the private Unix channel. No personal Auth token,
second account approval or per-install administrator login is forwarded. Existing
administrator calls remain supported, and service credentials gain no native
account/key administration.

An installer cannot change an existing binding’s product or origin. A live product
claim by another deployment conflicts; a retired association can be replaced only
after the old registry record and deployment claim are removed. PUT and list
responses include `trustRevision`, the product’s current trust-head revision, for
conditional updated/reinstalled pairing. Product proofs, acknowledgement,
conditional/idempotent mutations and exact managedBy checks remain mandatory.

## Pairing and recovery flow

1. An admin creates the SDK `SuitePairingRequest`: `contractVersion: "1.0"`, the
   actual `productId`, approved `baseUrl`, technical `purpose`, and optional bound
   `managedBy`. Initial pairing uses If-None-Match; authorized re-pairing uses the
   current association revision from service metadata.
2. HTTP 201 returns the SDK intent, fresh nonce, one-time secret and expiry, with
   an extra opaque `revision` for conditional cancellation. Maximum lifetime is
   ten minutes. Only one unresolved intent exists for that product at a time.
   Deliver it and trusted Backend connection information through a protected
   local participant receiver described in [automatic setup](suite-local.md#participant-receiver-for-gatewayyard-adaptation).
3. The participant computes the fingerprint of its actual complete declaration
   using SDK JCS/SHA-256. The local HTTP logical origin omits the TLS fingerprint.
   Decode the intent secret from base64url into raw bytes before calling
   `CreateSuitePairingProof` / the equivalent native SDK helper. The binding
   includes contract version, pairing ID, nonce, product ID, declaration fingerprint,
   approved logical origin; none may be substituted.
4. POST the SDK proof to `/complete` with a durable idempotency key. Backend checks
   proof/current creator authority before local contact, verifies the product
   through its bound socket, then checks everything again in
   the commit transaction. HTTP 200 returns `SuiteProvisioning`: scoped service
   identity/credential, issuer, configured Backend origin and fixed keys URI.
5. Persist the credential before POST `/ack` using **that credential**, not the
   admin key. Acknowledgment activates the technical association and deletes the
   recoverable pairing/provisioning secrets. The response includes current service
   identity. It still does not register the product or issue user delegations.

If `/complete` is lost, resend the same proven exchange. Recovery returns the same
provisioning result after restart and cannot create another credential. Competing
proofs cannot bind the same intent to different keys/origins. After acknowledgment,
cancellation or expiry, proof replay cannot establish new authority. An unresolved
expired attempt requires a fresh authorized reconciliation and intent.

During authorized replacement, the old association remains usable until the new
participant persists its credential and acknowledges. Acknowledgment atomically
retires all old generations, changes the head association and records invalidation.
Changing a native web address never silently resets local association trust.

## Rotation, signing keys and time

Credential rotation input is the SDK `{ "contractVersion": "1.0",
"credentialRevision": "<current>" }`, agreeing with If-Match. HTTP 201 returns the
replacement `serviceCredential`, nested identity and `acknowledgeBy`. Persist it
and acknowledge using the replacement credential. The old generation overlaps for
at most five minutes and is retired earlier on acknowledgment. Pending replacement
credentials cannot rotate/enroll other services. Expired/failed recovery does not
revive an old generation; an authorized admin can reconcile credential loss.
Full service revocation invalidates every generation and requires explicit
re-pairing to restore the revoked association. Credential-only revocation retains
the association for explicit authorized credential recovery.

Signing-key preparation publishes the new public ES256 key and records a key-set
change before an administrator activates it. Activation retires the old private
signer while retaining its public verification key through the five-minute maximum
outstanding delegation lifetime. Explicit revocation removes trust immediately and
records revoked key IDs. Revoking the last active key does not cause a restart to
silently generate a replacement; prepare and activate a new key explicitly.

Times are UTC with millisecond precision. Admission uses the shared five-second
clock safety margin and monotonic/high-water bounds; it does not extend an original
expiry after clock rollback. Significant rollback fails closed until synchronized
time is restored. Expiry is checked on every call independently of the 30-second
housekeeping cadence. Pairing/rotation timers concern service provisioning, not a
person's work session or Google login. See [user/probe issuance](suite-authorization.md) for delegation behavior.

## Persistence, backup and state

Migration 5 adds trust metadata to the **same SQLite database and `/data` volume**.
Existing user/key/session/vault/blob data and historical migrations are preserved.
SQLite contains credential verifiers, public keys and opaque secret handles.
Suite private keys, pairing secrets and recoverable responses are AES-256-GCM
files in `/data/secrets/suite/`, with a private `master.key`. Directories are 0700
and files 0600. No service secret is stored in the frontend, image, URL, event,
ordinary management view or raw idempotency JSON. Existing legacy personal-key
storage is unchanged by this Suite-specific implementation.

**Back up all of `/data` consistently**, including SQLite/WAL and the complete
secret store, with Backend stopped or an equivalent consistent snapshot. A database
copy alone cannot restore trust. Missing/malformed master or referenced secret
files fail startup rather than regenerate authority. Restore matching data and
secrets together. Older binaries reject migration 5; rollback requires the retained
pre-upgrade full backup and matching images, never deletion of migration records.

Encrypted files are installed before their metadata commit; failed mutations
roll back their state, outcome and invalidation together. Ambiguous commit failure
preserves files for reconciliation. Startup validates references and collects only
uncommitted file orphans under the same SQLite write reservation as secret creators.
Persistent invalidations are in `suite_trust_events`; registry mutations and
public event delivery are implemented by the [registry APIs](suite-registry.md).

The native frontend reports `not_configured`, `not_paired`, `pairing_pending`,
`paired` (shown as **Service paired · Registration pending**), `registered`
(with availability tracked separately) or `recovery_required`.
Saving/removing an address does not itself pair/revoke/uninstall a service.
Use the trust APIs above or the [native Suite controls](suite-administration.md)
for pairing, registration, key/credential rotation, revocation and recovery. Portal
is not required and does not replace the Backend frontend.

## Errors and verification

Errors use `{ "error": { "code": "...", "message": "...", "retryable": false } }`.
Examples: 400 `INVALID_REGISTRATION`, 401 `UNAUTHENTICATED`, 403 `FORBIDDEN`, 409
`REGISTRATION_CONFLICT` / `PAIRING_CONFLICT`, 412 `REGISTRATION_PRECONDITION_FAILED`,
422 `UNSUPPORTED_INTEGRATION`, 428 `PRECONDITION_REQUIRED`, and retryable 503
`PRODUCT_UNREACHABLE` / `INTEGRATION_UNAVAILABLE`. Responses are JSON/no-store and
never HTML/login redirects. Missing local resources and wrong methods use explicit
404/405 errors without revealing private details before authorization.

Go tests exercise real Unix service transport plus HTTP/TLS native boundary rejection, SDK proof/token verification, restart/lost responses, expiry, wrong identities/origins/declarations, credential separation, concurrency, revocation and rollback. Split-image/browser tests retain native login, roles, data operations and independent frontend replacement. The packaged local/browser flow is checked by `scripts/check-suite-proxy.py`; CI/release retain race tests and CI also runs vet. See [same-host validation and delivery](suite-local.md#validation-and-delivery-boundary) for the limits of this evidence.
