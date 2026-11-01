# Suite registry, events and availability

Backend work point 5 implements the published NoX MCP 0.7.0 / Suite 1.0
[contract references](suite-migration.md#approved-references-and-progression).
[Persistent pairing and service trust](suite-trust.md) are prerequisites for
membership. Gateway, Yard adaptations, Portal and CLI are separate product work.
[User/probe issuance](suite-authorization.md) is implemented in point 6, and
[Backend's own Suite MCP path](suite-product.md) in point 7.
Any present or future conforming product can use these APIs; there is no product
catalog encoded in the registry.

## Registration and reads

All routes use the existing Suite HTTPS/trusted-proxy boundary and JSON/no-store
responses. Native HTTP administration and legacy clients retain their behavior.
Setting an address in the dashboard only stores configuration; it does not pair,
register or verify a destination. A completed pairing establishes trust; explicit
registration establishes membership.

| Operation | Route | Authority and result |
| --- | --- | --- |
| Complete registry snapshot | `GET /api/nox/products` | Active user or authorized service; filtered as described below |
| One registration | `GET /api/nox/products/{productId}` | Authorized record or safe user summary; absent authorized read returns 404 `PRODUCT_NOT_REGISTERED` |
| Create | `PUT /api/nox/products/{productId}` | Admin or authorized installer; `If-None-Match: *`; 201 with accepted record |
| Update | Same PUT | Same authority; `If-Match: "revision"`; 200, including verified identical configuration |
| Remove | `DELETE /api/nox/products/{productId}` | Same authority; `If-Match: "revision"`; empty 204 |
| Event page/long poll | `GET /api/nox/events?after=CURSOR&waitMs=25000&limit=256` | Active service with `events.read`; not a user's session/key |
| Obtain/reset producer epoch | `POST /api/nox/products/{productId}/observation-epochs` | Corresponding reporting service; SDK epoch-request body; 201 |
| Report availability | `POST /api/nox/products/{productId}/observations` | Corresponding reporting service; SDK observation body; 200 with SDK accepted/envelope result |

All mutations require `Idempotency-Key`. Registration requests use the published
`{ "contractVersion": "1.0", "baseUrl": "https://approved-origin", "managedBy"?: ... }`
shape. The authenticated installer must own the exact administrator-approved
product/deployment/origin binding; `managedBy` cannot self-grant ownership.
Gateway and ordinary product credentials cannot enroll/remove products. An admin
can manage foundations without `managedBy`, or explicitly approved managed products.

Registration resolves the current acknowledged pairing, verifies the HTTPS target
and proven SPKI pin, and fetches `/nox/integration` again. It checks the route's
product ID, contract and required features, preserves unknown optional declaration
fields, and never forwards the caller's key/cookie/service token. Existing network
allow lists, no-redirect policy, fetch budgets and concurrency bounds apply.
Caller authority, trust association and conditional revision are rechecked in the
commit transaction, after the network fetch. An unpaired, revoked or substituted
participant cannot acquire membership by serving JSON.

Material declaration/configuration/association changes generate a fresh opaque
registration revision and registry revision. An identical verified update preserves
both and emits no duplicate membership event. Heartbeats do not change membership
or these revisions. No network outage or stale observation deletes a product.

Admin and `registry.read` service snapshots include complete accepted records,
observations, `registryRevision` and a cursor from one consistent SQLite transaction.
A product with `events.read` can obtain the same shape restricted to its own product,
so it can reconcile after cursor expiry. Ordinary active users receive only product
ID, display name, product version and summarized availability; no internal origins,
association/deployment IDs, unknown extensions, event cursor or raw diagnostics.
A stable privileged single-record GET returns the strong revision ETag. User
summaries have no such ETag. Successful enriched PUTs have no ETag/Last-Modified.

## Transactions, retries and removal

The registration/removal, idempotency outcome and events commit atomically.
Concurrent mutations cannot create independent results for the same key. Recognized
retries return the original committed result before rejecting a now-stale revision
and without re-fetching a potentially changed/offline product. Changed contents
with the same key return 409. Current caller authority is always required.
Outcomes and removal tombstones are retained at least 24 hours across restart.
After retention, reconcile state before submitting a new operation/key.

Removal does not delete application data. It retires the named registration and
its association, revokes that association's credential generations and emits the
registration/association tombstone. An already paired replacement is not revoked
by removal of its predecessor's record. A delayed original removal replay returns
its original 204; it cannot delete a later registration. Fresh enrollment after
removal requires an explicitly proven active association and receives new IDs.

## Events and reconciliation

The feed includes committed `product.upserted`, `product.removed`,
`service.invalidated`, `signing-keys.changed`, `observation.updated`, and
`producer.reassociated` events. Native user/key changes also emit `authorization.invalidated` atomically as part
of [point-6 issuance/invalidation](suite-authorization.md).
It uses management HTTP JSON long polling, not MCP SSE.

`waitMs` accepts integers 0–25000 and defaults to 25000. `limit` accepts 1–256 and
defaults to 256. Pages are bounded by count and payload size. Empty successful
responses are normal. Backend allows one concurrent feed request per service and
bounds concurrent feed work; use a shared subscription, not one poll per user/tool.
A waiting request holds no database transaction. Committed local changes wake it;
periodic checks also see cross-process commits and current credential revocation.
The HTTP request budget is 30 seconds for this route, allowing the agreed wait.

Cursors are opaque, encrypted/authenticated and bound to the service's current
association/grant scope and the persistent event-log generation. Never compare or
edit their spelling. A page advances past scanned unauthorized events without
revealing their contents. Registry readers receive registry/observation events;
ordinary products receive their own registry/observation changes. Trust and
signing/authorization invalidations needed for verification reach event readers.
No event contains raw credentials. Process events in order and persist the cursor
after applying them. `hasMore: true` requires immediate bounded draining.

Retention is seven days or 100,000 events, whichever bound is reached first,
independent of mutation-idempotency retention. An unknown, modified, wrong-scope,
truncated, future or wrong-generation cursor returns 410 `EVENT_CURSOR_EXPIRED`.
Obtain a complete authorized `/api/nox/products` snapshot, atomically replace local
registry/observations/cursor, clear potentially missed authorization state, then
resume the feed. Do not merge an incomplete snapshot or retain absent products.
Normal Backend restart preserves generation and resumable cursors.

After restoring/replacing authority state from backup, stop the Backend, restore
all of `/data` together, synchronize the system clock, and explicitly invalidate
old cursors before starting services:

```sh
nox-backend reset-suite-event-cursors
```

Run in the Backend image with the restored `/data` mounted and the normal data-dir
configuration. This changes only the event-log generation; issuer, keys, service
credentials, membership, revisions and event contents remain. Consumers must
reconcile. Do not run this on routine restart. Do not restore SQLite alone without
the matching private secret store; see [backup requirements](suite-trust.md#persistence-backup-and-state).

## Observations

Only current paired reporting services can submit observations. Gateway reports
require `mcp.report`; installer lifecycle reports require `lifecycle.report` plus
the exact currently authorized `managedBy` deployment. Admin/user credentials do
not impersonate a reporting service. Route/product, registration, association,
producer ID, source and Backend-issued epoch must match current authority.

Obtain an epoch with the SDK `SuiteObservationEpochRequest`. Persist the returned
epoch and sequence state before reporting. Loss/reset of producer sequence state
requires a new authorized epoch and actual-state reconciliation; it does not enroll
or revive a product. Metadata/association changes discard obsolete observations;
reconcile the current registration and obtain a current epoch before reporting.

Sequence values are canonical uint64 decimal **strings**, compared numerically by
the released SDK (including values above JavaScript's exact integer range). Older
and identical duplicates return `accepted: false` without another observation event.
The same epoch/sequence with different content returns 409 `OBSERVATION_CONFLICT`.
Retired epochs/revisions/associations cannot update current observations.

Backend persists receive time and `freshUntil` as the earlier of receive/observed
time plus freshness. Reports over five seconds in the future are rejected. A delayed
old report does not become fresh on arrival. Consumers derive `unknown` at expiry;
no synthetic sequence/event or membership deletion is generated by time passing.

| Setting | Default |
| --- | --- |
| `NOX_BACKEND_SUITE_REPORT_INTERVAL_MS` | `15000` |
| `NOX_BACKEND_SUITE_OBSERVATION_FRESHNESS_MS` | `45000` |

Both accept positive integer milliseconds and support the usual `NOX_SYNC_*`
fallback. Compose passes them through. Privileged snapshots expose these deployment
settings in optional `observationPolicy` (`reportIntervalMs`, `freshnessMs`) for
future reporters to consume. Backend does not start a Gateway/Yard probe loop.

Yard states: `starting/running/stopping/stopped/restarting/recreating/updating/unknown`.
Gateway states: `ready/unreachable/notReady/incompatible/unknown`. They remain
separate in snapshots and user summaries. A running container does not establish
MCP readiness, and stale Yard information does not override a fresh Gateway check.
A failed connection must still fail that call immediately; stored readiness is
an observation, never permission to execute a tool.

## Installed data and native UI

Migration 6 is additive on the same SQLite database and `/data`. It adds registry,
tombstones, event-log generation/floor, producer epochs and observations. Migration
5 trust, existing accounts, sessions, keys, vaults and blobs are preserved. Complete
backup/rollback rules remain in the storage guide. An older binary cannot safely
open the newer schema; rollback requires its matching full data backup.

Native connection statuses include `registered` once current pairing and registry
association match. The label explicitly leaves availability separate. Complete
[native pairing/registration controls](suite-administration.md) are implemented
in point 8 and work without Portal or Gateway. A saved random HTTPS address remains
configuration only.
