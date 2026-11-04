# Same-host Suite integration and automatic foundation setup

This implements the Backend part of **4. Completar a configuração inicial no Backend**, including its remaining work under **2. Corrigir o transporte e a configuração dos endereços**. It consumes NoX MCP `v0.7.3`, wire contract `1.0`, and follows the [same-host integration record](https://github.com/mapherez/nox-mcp/blob/v0.7.3/Documents/SUITE_SAME_HOST_INTEGRATION.md). The original Suite implementation plan is not rewritten. The subsequent cleanup removes the obsolete server executable alias, unused network-policy configuration and manual secret-transfer browser controls; the local participant protocol remains available.

## Runtime and deployment

Participating product services use HTTP over Unix sockets in a shared **local** Docker volume. They must run on the same Docker host. No service TCP connection, HTTPS certificate, private/loopback IP or forwarded header grants this channel. Existing native HTTP/HTTPS APIs, OAuth, data and frontend behavior remain separate.

The supplied production and development Compose files mount the same named volume `nox-suite-local` into Backend at `/run/nox-suite`. `NOX_SUITE_LOCAL_VOLUME_NAME` can select another installation's volume name; every participant must use that same value. The driver is local with no remote mount options. Do not bridge sockets over TCP or share this volume across hosts. Backend does not receive the Docker socket.

The image prepares the directory as UID/GID `10001`, mode `3770`. Participant images/deployments must use supplementary group `10001` (or an equivalent deliberately configured ownership arrangement), with distinct service users where possible. Only services that need the channel mount it; the native frontend does not. Socket files use mode `0660`. The listener holds an exclusive ownership lock, refuses ordinary files/symlinks and active listeners, recovers stale sockets, and removes only its own socket on shutdown.

Backend serves `/run/nox-suite/nox-backend.sock`. It exposes `/nox/integration`, the existing `/api/nox/*` service protocol, and opt-in `/mcp/suite` there. The native TCP listener and frontend refuse service calls to `/api/nox/*` and `/mcp/suite`, including through TLS proxies. Human administrator operations use authenticated `/api/admin/*` routes with the existing active-role, personal-key/session and CSRF checks. Service credentials cannot use that adapter.

`NOX_BACKEND_SUITE_SOCKET_DIR` selects the channel directory. Docker images/Compose set `/run/nox-suite` automatically. Bare binary execution leaves it disabled by default, preserving native startup; an explicit directory enables it. The obsolete Suite target CIDR/port/proxy variables and their validation have been removed. Delete them from deployment configuration; they have no effect on the local channel. Native TLS options remain independent.

## Product identity and web URLs

The native URL entered in Service connections is retained for opening the product. Backend does not fetch that web URL to infer host locality. Verification contacts the intended product's local socket and checks the actual declaration.

The normal logical origin is `http://<productId>` and maps only to `<productId>.sock`, with no DNS lookup, environment proxy or TCP fallback. To cover every SDK-valid ID, numeric-only IDs use `http://<id>.nox-suite`; a 64-character ID is split after its first 32 characters into two labels and suffixed with `.nox-suite`. The mapping is canonical and reversible; ordinary IDs such as `nox-yard` and `nox-backend` keep their readable origin. These are protocol/audience identities, not web addresses. Changing a web URL does not silently replace a service identity or association.

## Dashboard workflow

1. Save the native Yard/Gateway HTTP or HTTPS address under **Service connections**.
2. Select **Verify Yard/Gateway**. Backend fetches the local declaration, checks identity and records an expiring, configuration-bound verification. This does not enroll the service.
3. Select **Connect Yard/Gateway** and confirm the verified identity/version. Backend creates the existing SDK pairing intent, delivers it to the local participant, waits for actual credential acknowledgement and completes verified registration.
4. The card reports the actual state. A receipt alone produces `waiting`, not `registered`. Retry with **Resume connection** after uncertain delivery, or cancel before editing/removing a pending connection. The background reconciler finishes registration after a delayed acknowledgement without redelivering intents.

No protocol JSON, certificates, service credentials, CIDRs or proxy settings are copied by the user. A participant without the new local receiver reports an explicit adapter-update error. Current unadapted Yard/Gateway releases are not claimed to work merely because their native web URLs load.

Native APIs:

| Route | Purpose |
| --- | --- |
| `PUT /api/admin/connections/{service}` | Save web URL with the existing configuration precondition |
| `POST /api/admin/connections/{service}/verify` | `{ "revision": "<configuration revision>" }`; verify the local identity |
| `POST /api/admin/connections/{service}/connect` | `{ "attemptId": "<verified attempt>" }`; confirm or resume the same operation |
| `POST /api/admin/connections/{service}/cancel` | `{ "attemptId": "<attempt>" }`; cancel unacknowledged setup |
| `GET /api/admin/connections` | Safe configuration, registration and setup progress |
| `/api/admin/suite/operations/{operation...}` | Active-admin adapter for existing advanced Suite management operations |

The setup view contains identity/version, state, non-secret attempt ID, bounded diagnostics and available actions. It contains no intent, nonce, proof, credential or private-store handles. Setup metadata uses non-destructive database migration `008_suite_connections.sql`; pairing/provisioning secrets remain in the existing encrypted Suite vault. Pairing ID and setup association are committed atomically before delivery. Conditional configuration checks, leases, current authority and durable operation keys prevent stale/duplicate work. Other active admins can cancel pending setup or finish an already acknowledged association; an inactive creator does not require manual database editing.

## Participant receiver for Gateway/Yard adaptation

The service owns its socket and serves `GET /nox/integration`. Add **`POST /nox/integration/pairing` on that local listener**, bounded to the contract body limit. Backend sends:

```ts
type PairingDelivery = {
  contractVersion: "1.0";
  backendOrigin: string; // http://nox-backend in this local installation
  intent: SuitePairingIntent; // complete existing SDK document, as an object
};
```

`Idempotency-Key` is the stable setup attempt ID. Personal keys, browser cookies and authorization headers are never forwarded. Do not redirect delivery. The receiver must allow concurrent declaration reads while processing delivery: Backend verifies `/nox/integration` during the completion callback.

The receiver must validate the envelope/SDK intent, its own product/origin and expiry; use the existing declaration fingerprint and HMAC helpers with no TLS fingerprint for HTTP logical origins; call `/api/nox/pairings/{pairingId}/complete` through `nox-backend.sock` using a durable idempotency key; persist provisioning/credential privately; then call `/ack` with that credential. Persist receiver recovery state so repeat delivery resumes the same intent rather than issuing another association. Trust and grants remain Backend-owned.

Return `200`, `202` or `204` only for accepted delivery; Backend still requires its own actual acknowledgement state before registration. Missing receiver (`404`/`405`) produces `UNSUPPORTED_INTEGRATION`. A timeout requires reconciliation, not another independent pairing. Existing stored network associations are not silently converted into local ones.

Yard's subsequent adaptation prepares this channel through its existing deployment/jobs/configuration/recovery system and initiates registration of installed products. Gateway consumes the authorized registry and uses the same local mapping. No additional installer, certificate service or product catalog is introduced here.

## Point 5 — Preservar os usos existentes do Backend

The compatibility review compares the local-channel correction with released
Backend `v1.0.15` (`577f461`). It covers native contracts for every consumer;
existing products do not need Suite membership to keep using those contracts.

| Existing use | Review and regression evidence |
| --- | --- |
| Native frontend and administration | Existing dashboard, Google login/callback/logout, session cookie, legacy forms, account/key/user/vault operations and ownership rules remain. The correction removes an accidental `setup` field from the native user-status request; unknown fields are rejected without changing the account. |
| External HTTP clients | `/v1/*` routes, Bearer and legacy query-key authentication, key rotation, disabled users, file/download responses and synchronization are retained. The new service transport does not restrict these clients to the Backend host. |
| Direct MCP | `/mcp`, its catalog/CLI metadata, personal-key authentication, ownership, structured results and integer revisions retain their native behavior. `/mcp/suite` has the separately approved local-only policy. |
| Installed data | Migration 8 adds only the local setup journal. A dedicated version-7 upgrade/restart regression preserves personal keys, browser sessions, committed file revisions/blobs, saved native URLs/revisions, existing service credentials/identities, issuer/signing keys, secret-store master key and migration history. Existing associations are retained as data, not silently converted into working local associations. |
| Deployment | Existing native port, public/OAuth origin, `/data`, data volume, `nox-backend` executable and environment-variable fallback remain. The new Suite volume is additional and is not mounted into the frontend. |

Read-only inspection of available sibling sources confirmed the existing sync
client's `/v1/sync/*` usage and the wiki importer’s auth/vault/file/download
contracts. This is source compatibility evidence, not a claim that every
external consumer or its complete frontend workflow was run. No sibling product,
original implementation plan, publication or installed data is changed by point 5.

Point 5 is complete in source. After the compatibility fix, the full
`go test -race -count=1 -mod=readonly ./...` suite and `go vet -mod=readonly ./...`
passed. Existing CI/release jobs already run the new regressions; no workflow
change is required. The owner reported successful GitHub builds for point 4 and
its temporary-path fix; these latest changes still need their own GitHub run.
Point 6, **Adaptar o Gateway ao mesmo processo de configuração**, remains next.

## Validation and delivery boundary

Go tests use real Unix sockets and cover automatic pairing/ACK/registration, identical retries after lost delivery, receipt loss after ACK, process restart and background reconciliation, stale configuration, cancellation, active-admin/lease checks, native HTTP administration and rejection of TCP/TLS service calls. Filesystem/listener ownership and all SDK-valid ID shapes are checked separately. Existing native regressions remain in the full race/vet suite.

`check-suite-proxy.py --browser` exercises packaged Backend/frontend images and an explicitly disposable local protocol participant. It performs setup in an actual insecure HTTP browser context, drops a response after server commit, reloads, checks registration without duplicate delivery and rejects network service routes. It preserves authority through restart and deletes test containers/network/volume. Native split-deployment/browser and image-version checks remain required in CI/release. The test participant is not a shipped Yard implementation.

Review, CI, publication and installed rollout remain separate. Update the existing Compose definition as well as the images to add the shared volume; image pull/recreate alone cannot add mounts. Preserve the existing `/data` volume, project, native ports, OAuth origin and settings. Back up the full data directory before the schema upgrade; do not use `down -v`. Gateway/Yard/CLI adaptation and actual Suite end-to-end validation remain later points in the fixed correction sequence.
