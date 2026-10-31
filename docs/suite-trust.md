# Suite authority, pairing and service trust

This implements Backend work point 4 against the published NoX MCP `0.7.0`
contracts, wire version `1.0`. The [approved work sequence](suite-migration.md)
links the authoritative Gateway/integration documents. These APIs are HTTP
management operations, not new MCP methods. They work for any conforming product;
`gateway`, `installer` and `product` are technical purposes, not a product catalog.

Backend now persists its issuer, ES256 signing keys, verified service associations,
credential generations, pairing recovery and conditional/idempotent outcomes.
[Registry membership/event delivery](suite-registry.md) is implemented in point 5.
User/probe token issuance (6), Backend Suite MCP adaptation (7) and complete
UI/delivery integration (8) remain pending.
A paired service is not a registered product and has no integrated execution rights.
Existing direct `/mcp`, `/v1/*`, Google login and native account/vault administration
retain their own authentication and behavior.

## Transport and target policy

Suite trust routes require a configured HTTPS `NOX_BACKEND_PUBLIC_URL` and either
actual TLS at Backend or an explicitly trusted TLS proxy boundary. A private IP or
caller-supplied forwarding header does not establish HTTPS trust. HTTP native
administration remains available under its existing policy; it cannot provision
Suite secrets through these routes.

The existing Compose files pass these optional Backend settings, including the
usual `NOX_SYNC_*` fallbacks:

| Setting | Meaning |
| --- | --- |
| `NOX_BACKEND_SUITE_ALLOWED_CIDRS` | Explicit approved target networks; no implicit LAN/loopback trust and no outbound verification if empty |
| `NOX_BACKEND_SUITE_ALLOWED_PORTS` | Approved destination ports, comma-separated; default `443` |
| `NOX_BACKEND_SUITE_TRUSTED_PROXY_CIDRS` | Exact trusted Backend-facing proxy addresses/networks; empty by default |
| `NOX_BACKEND_TLS_CERT_FILE` / `NOX_BACKEND_TLS_KEY_FILE` | Optional direct HTTPS certificate/key paths; configure both together |
| `NOX_BACKEND_TLS_CA_FILE` | Optional extra trusted CA file for the packaged HTTPS healthcheck |

For example, an administrator can explicitly approve `192.168.10.0/24` and ports
`443,8443`. This permits conforming products in that deployment without hardcoded
product names. Use the actual deployment's approved ranges/ports.

Pairing intent creation validates origin syntax/port/policy without requiring a
not-yet-started product's DNS to resolve. Completion resolves and validates **all**
target IPs before fetching, repeats that validation for every connection, and
connects directly to an approved IP. HTTP proxy environment variables are ignored.
The Backend never forwards an administrator's cookie/key, a service credential or
the pairing secret to the product declaration endpoint. Redirects are rejected.

Verification uses a TLS leaf **SPKI SHA-256 pin authenticated by the pairing HMAC**,
checks certificate validity time and rejects a different key. This is the approved
explicit pin trust mode, including for private-CA/self-signed deployments; it is
not trust-on-first-use or unchecked TLS. A renewed certificate with the same proven
public key can retain the association; a changed public key requires authorized
re-pairing. Public keys come from the configured Backend, never token-header URLs.

Each operation has a 20-second budget. Product verification additionally bounds
DNS (2 seconds), connect/TLS/header waits (3 seconds each), total HTTP time
(10 seconds), response size (2 MiB) and concurrent verifications (8). SDK parsing
rejects duplicate keys, invalid Unicode, excessive nesting, incompatible versions
and unsupported required features. Unknown optional data is preserved and inert.

### TLS deployment choices

**Direct Backend TLS:** mount readable certificate/key files into Backend and set
both TLS file settings. Use `https://...` for the frontend's internal
`NOX_FRONTEND_BACKEND_URL`; the certificate must match that hostname. For a private
CA, mount an appropriate trusted CA bundle at the frontend's existing
`/etc/ssl/certs/ca-certificates.crt`. Backend healthcheck connects locally with TLS,
verifies the configured public hostname and uses the optional CA file. No extra
application port is required. TLS files are deployment secrets, never image assets.

**Trusted reverse proxy:** the external proxy must terminate actual HTTPS and
replace incoming forwarding claims with its verified scheme. Configure
`NOX_FRONTEND_SUITE_TRUSTED_PROXY_CIDRS` with its actual frontend-facing source
addresses. Frontend sets `X-Nox-Suite-Transport: https` only for that verified ingress
and overwrites a client's version of that header. Configure Backend's trusted-proxy
CIDRs with the actual frontend-facing addresses. Backend requires both the trusted
peer and the sanitized transport/scheme claims. Keep these plaintext internal hops
within the explicitly trusted boundary; prefer exact proxy IPs over broad networks.
The native frontend's public HTTP port rejects Suite requests even if a caller
spoofs `X-Forwarded-Proto` or `X-Nox-Suite-Transport`.

These settings do not automatically install certificates, configure an external
proxy, or make an unchanged product support pairing. Existing installations need
no new variables to keep using the native UI/client APIs. Suite operation requires
the approved HTTPS/target configuration above.

## Caller authority and service grants

Administrator calls use an existing active Google-backed session or personal
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
A Gateway cannot enroll services. An installer can request only `product` pairing
for its exact administrator-authorized deployment, product ID and origin. Its
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
| Read issuer/public keys | `GET /api/nox/keys` | Trusted HTTPS origin; public JSON and key-set ETag |
| Prepare signing key | `POST /api/nox/keys` | Admin; If-Match key-set revision; returns prepared key ID and new revision |
| Activate prepared key | `POST /api/nox/keys/{keyId}/activate` | Admin; If-Match current key-set revision |
| Revoke signing key | `DELETE /api/nox/keys/{keyId}` | Admin; If-Match key-set revision |
| Create intent | `POST /api/nox/pairings` | Admin or scoped installer; initial/replacement association condition |
| Read safe pairing state | `GET /api/nox/pairings/{pairingId}` | Admin or its authorized creating installer; no secret |
| Complete/recover proof | `POST /api/nox/pairings/{pairingId}/complete` | Bound HMAC proof over trusted HTTPS |
| Acknowledge persisted provisioning | `POST /api/nox/pairings/{pairingId}/ack` | Newly issued service credential |
| Cancel pending intent | `DELETE /api/nox/pairings/{pairingId}` | Authorized admin/installer; If-Match pairing revision |
| Read services / one service | `GET /api/nox/services`, `GET /api/nox/services/{serviceId}` | Admin list; admin or service for one record |
| Revoke full association/service | `DELETE /api/nox/services/{serviceId}` | Admin/current service; If-Match association revision; all generations revoked |
| Rotate service credential | `POST /api/nox/services/{serviceId}/credentials` | Admin/current service; If-Match credential revision |
| Acknowledge rotation | `POST /api/nox/services/{serviceId}/credentials/{credentialId}/ack` | Replacement credential only |
| Revoke credential generation | `DELETE /api/nox/services/{serviceId}/credentials/{credentialId}` | Admin/current service; If-Match target credential revision |
| Read installer deployment grants | `GET /api/nox/services/{serviceId}/deployments` | Admin or that scoped installer |
| Authorize/change/remove deployment | `PUT` / `DELETE /api/nox/services/{serviceId}/deployments/{deploymentId}` | Admin; initial/current deployment revision |

SDK-defined pairing/proof/provisioning/rotation payloads retain their published
schemas. The key-management, service-state, deployment-authorization and rotation
acknowledgment routes are Backend adapters; they do not introduce MCP extensions.
Public key-set JSON contains `contractVersion`, `issuer`, `revision`, and `keys`.
Service reads expose `revision` for the association and nested `service` containing
the current credential's ID/revision/grants, without its bearer secret.

Deployment authorization input is `{ "productId": "<actual-product-id>",
"baseUrl": "https://approved-origin" }`. It authorizes that installer/deployment;
it is not registry membership, a Docker schema or permission to enroll other targets.

## Pairing and recovery flow

1. An admin creates the SDK `SuitePairingRequest`: `contractVersion: "1.0"`, the
   actual `productId`, approved `baseUrl`, technical `purpose`, and optional bound
   `managedBy`. Initial pairing uses If-None-Match; authorized re-pairing uses the
   current association revision from service metadata.
2. HTTP 201 returns the SDK intent, fresh nonce, one-time secret and expiry, with
   an extra opaque `revision` for conditional cancellation. Maximum lifetime is
   ten minutes. Only one unresolved intent exists for that product at a time.
   Deliver it and trusted Backend connection information through a protected
   file/secret mount or equivalent explicit manual configuration.
3. The participant computes the fingerprint of its actual complete declaration
   using SDK JCS/SHA-256, and the base64url SHA-256 of its TLS leaf's DER SPKI.
   Decode the intent secret from base64url into raw bytes before calling
   `CreateSuitePairingProof` / the equivalent native SDK helper. The binding
   includes contract version, pairing ID, nonce, product ID, declaration fingerprint,
   approved origin and TLS fingerprint; none may be substituted.
4. POST the SDK proof to `/complete` with a durable idempotency key. Backend checks
   proof/current creator authority before network access, verifies the product
   over its proven TLS key and approved target, then checks everything again in
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
Changing a TLS key or address never silently resets trust on reconnect.

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
person's work session or Google login. User/probe token issuance remains point 6.

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
Persistent invalidations are in `suite_trust_events`; public event delivery and
registry mutations and event delivery are implemented by the [point-5 APIs](suite-registry.md).

The native frontend reports `not_configured`, `not_paired`, `pairing_pending`,
`paired` (shown as **Service paired · Registration pending**), `registered`
(with availability tracked separately) or `recovery_required`.
Saving/removing an address does not itself pair/revoke/uninstall a service.
Use the trust APIs above for pairing and revocation. Full native management
controls are completed in the planned administration/delivery work; Portal is not
required for these APIs and does not replace the Backend frontend.

## Errors and verification

Errors use `{ "error": { "code": "...", "message": "...", "retryable": false } }`.
Examples: 400 `INVALID_REGISTRATION`, 401 `UNAUTHENTICATED`, 403 `FORBIDDEN`, 409
`REGISTRATION_CONFLICT` / `PAIRING_CONFLICT`, 412 `REGISTRATION_PRECONDITION_FAILED`,
422 `UNSUPPORTED_INTEGRATION`, 428 `PRECONDITION_REQUIRED`, and retryable 503
`PRODUCT_UNREACHABLE` / `INTEGRATION_UNAVAILABLE`. Responses are JSON/no-store and
never HTML/login redirects. Missing local resources and wrong methods use explicit
404/405 errors without revealing private details before authorization.

Go tests exercise real HTTP/TLS plus SDK proof/token verification, restart/lost
responses, clock/expiry boundaries, wrong pins/origins/declarations, denied targets,
credential separation, concurrent completions, revocation, scoped deployments and
transactional failure rollback. Existing split-image/Chromium tests retain login,
roles, vaults, sync/MCP and frontend-only replacement, and now reject forged Suite
TLS forwarding claims. `scripts/check-suite-proxy.py` additionally tests actual
trusted TLS ingress, direct HTTP spoof rejection, issuer persistence after container
restart and the packaged direct-TLS healthcheck. CI and release checks run these
image tests and Go race tests; CI also runs vet.
These protocol fixtures do not claim that Gateway, Portal, CLI or adapted products
already exist, or replace later installed-product delivery validation.
