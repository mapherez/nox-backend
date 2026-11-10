# Historical Backend implementation and validation record

This preserves implementation checkpoints and their evidence. It is not the current
installation, configuration or API reference. Entries describe behavior at their
recorded checkpoint, including superseded HTTPS-only service transport, native
Google login, SDK versions and work that was pending then. Do not apply those
statements as current setup instructions.

Current guides:

- [Overview](../README.md), [setup](user-setup.md) and [configuration](backend-configuration.md).
- [NoX Login](nox-login.md) and [same-host Suite setup](suite-local.md).
- [Backend MCP publication](suite-product.md), [delivery](suite-delivery.md) and [troubleshooting](troubleshooting.md).

Current Backend uses SDK 0.7.3 / Suite contract 1.0. Google credentials and admission
belong to Auth; service traffic uses the shared Unix channel. The chronological
records below do not override the current guides.

**Historical record of the earlier eight-point implementation.** These point numbers
are not the later 12-point correction sequence. Original Backend baseline: commit `a2382ec` (`v1.0.7`).
At that historical checkpoint the SDK was `github.com/mapherez/nox-mcp v0.7.0`
and the application package version was `1.0.14`; that point-8 implementation starts from
`b9f2793` and does not create a release. Suite wire compatibility remains `1.0`.

## Approved references and progression

The approved requirements and contracts are maintained in NoX MCP:

- [General implementation plan, including section 19](https://github.com/mapherez/nox-mcp/blob/81abc89c4494e75b097eec5d2dfc861f4937a003/Documents/NOX_SUITE_IMPLEMENTATION_PLAN.md#19-current-delivery-and-next-work-backend).
- [Product integration design](https://github.com/mapherez/nox-mcp/blob/81abc89c4494e75b097eec5d2dfc861f4937a003/Documents/PRODUCT_INTEGRATION_PROPOSAL.md).
- [Gateway and shared integration contract](https://github.com/mapherez/nox-mcp/blob/81abc89c4494e75b097eec5d2dfc861f4937a003/Documents/GATEWAY_CONTRACT.md).
- [Released SDK integration mechanisms](https://github.com/mapherez/nox-mcp/blob/81abc89c4494e75b097eec5d2dfc861f4937a003/Documents/INTEGRATION.md).

This document records Backend progress and compatibility evidence. It does not
replace or revise the approved decisions, wire contracts, or product scope.
Work stops after each numbered point until the owner approves the next one.

| Point | Work | State |
| --- | --- | --- |
| 1 | Record the existing baseline and consume SDK `0.7.0` | Completed and approved to proceed |
| 2 | Separate frontend/backend builds, images, containers, and business operations within the same project | Completed; owner confirmed split deployment working and authorized point 3 |
| 3 | Complete administration APIs and retain native frontend functionality | Completed; owner confirmed operation and authorized point 4 |
| 4 | Implement persistent service trust and pairing | Completed; owner confirmed installed operation and authorized point 5 |
| 5 | Implement registry and durable events | Completed; owner confirmed installed operation and authorized point 6 |
| 6 | Implement authorization issuance | Completed; owner confirmed installed operation and authorized point 7 |
| 7 | Adapt Backend as a Suite product | Completed; owner confirmed installed operation and authorized point 8 |
| 8 | Complete administration, deployment, documentation, CI, and delivery validation | Implementation and local delivery validation complete; owner publication/installed checks follow the delivery guide |

The Backend retains its native frontend and all current management functions,
plus initial service connection configuration required by the plan. Frontend
and backend remain in the same repository/project with separate builds, images,
and containers. Frontend-only updates must not require rebuilding the backend
image. Yard represents them as services of one `nox-backend` project. The Portal
remains a separate application and does not replace the Backend frontend.

## Existing compatibility baseline

| Area | Behavior to preserve during implementation |
| --- | --- |
| Native frontend | `/vault-dashboard`, its assets and forms, user/key/vault administration and downloads, and the existing frontend integration used by another product. At this baseline the assets/templates are embedded in the Go executable; separation is point 2. |
| Browser authentication | Google login through `/auth/google/start` and `/auth/google/callback`, logout through `/auth/logout`, approved-account restrictions, existing redirects, and `nox_sync_session` semantics. Session authority stays in Backend. |
| Client setup | The dashboard supplies the Server URL and personal API key used by NoX Sync and other integrations. Existing public URLs and authentication flows must remain usable after container separation. |
| Personal API keys | Existing per-user `noxsync_` keys, Bearer-header precedence over the legacy `api_key` query parameter, rotation and disabled-account rejection. Dashboard cookies do not authenticate the direct API/MCP path. |
| Stable HTTP API | Existing `/v1/health`, `/v1/info`, `/v1/auth/check`, vault lifecycle and `/v1/status` routes, methods, JSON/error formats, and runtime version resolution. |
| Synchronization | `/v1/sync/*`, event delivery, per-vault/client lock ownership, staged uploads, hash checks, atomic commits, conflict/tombstone handling, and stale-lock recovery. |
| File/read integrations | `/v1/files` and `/v1/files/download`, committed file contents, `expectedHash`/`expectedRevision` checks, existing download headers, and wiki import identity tied to the public URL. |
| Data ownership | Every vault operation is restricted to its owner's identity. `ADMIN` does not grant access to another user's vault data. |
| Direct MCP | Existing POST `/mcp` in HTTP JSON mode, public initialization/catalog/health/info, the eight authenticated tools, per-request identity checks, the 10 canonical names and `_meta.cli` paths, structured results/errors, and exact integer revisions. SDK installation alone does not enable Suite access. |
| Storage | SQLite, `/data`, blobs, staging, logs, existing keys/sessions/IDs/revisions, database-name compatibility, and the current migration history. Point 1 changes no migration or schema. |
| Deployment/configuration | Existing public URL/port, data mounts/volumes, UID/GID 10001, `NOX_BACKEND_*`/`NOX_SYNC_*` fallback rules, legacy executable, build/runtime version resolution, and packaged healthcheck. Later container separation follows the approved plan while preserving these integration contracts. |

Existing details remain in the [Stable Client API](client-api.md),
[direct MCP contract](mcp.md), [read API](read-api.md),
[configuration](backend-configuration.md), [setup](user-setup.md), and
[storage/update compatibility guide](backend-separation.md).

## Point 1 validation

Validation ran on macOS arm64 using Go `1.26.0` and Node `24.22.1`, and through
the existing Linux Docker image build. Tests used disposable data; no installed
service, production volume, API key, session, or application data was modified.

| Check | Result |
| --- | --- |
| Backend tests before dependency update | 67 top-level tests; 178 passing results including subtests/fuzz seeds; no failures |
| Backend tests after update, with the race detector | Same 67 top-level tests and 178 passing results; no failures or race reports |
| SDK tests under the Backend consumer dependency graph, with the race detector | 13 top-level tests; 421 passing results including subtests; no failures or race reports |
| Resolved module graph comparison | Only `github.com/mapherez/nox-mcp` changed, from `v0.6.1` to `v0.7.0`; no other selected version changed |
| Static/module checks | `go vet -mod=readonly ./...` and `go mod verify` passed |
| Existing release tooling tests | 39 passed using a canonical temporary directory, as described below |
| Existing Dockerfile build | Passed with `VERSION=step1-validation`; local image only |
| Packaged runtime checks | Passed: startup/storage, health/build version, executable healthcheck, info JSON, dashboard and assets, missing-credential HTTP error format, MCP initialization/catalog/metadata, public tool execution and protected-tool rejection |

To reproduce the Go checks, run these commands from `backend/`:

```sh
go test -race -count=1 -mod=readonly ./... github.com/mapherez/nox-mcp/go
go vet -mod=readonly ./...
go mod verify
```

On macOS, the existing release test fixtures compare Git's canonical repository
path with the temporary path. The default temporary path can contain `/var` or
`/tmp` aliases and fail that comparison; the metadata CLI entry-point comparison
has the same alias sensitivity. The initial run exposed this existing tooling
limitation. All 39 tests passed when rerun with a canonical temporary directory:

```sh
mkdir -p /private/tmp/nox-backend-release-tests
TMPDIR=/private/tmp/nox-backend-release-tests node --test scripts/release.test.mjs
```

Release script portability was recorded for point 8 and is resolved there; no
release script or workflow was changed in point 1. These local results do not claim GitHub CI
approval, multi-architecture image validation, or completed Suite service flows.
At the end of point 1, pairing, registry, issuance, Suite MCP and frontend
separation were still pending. The point 2 record below describes subsequent
progress. No application release was created by these local checks.

## Point 2 implementation

The owner selected **React + TypeScript + Vite+** for the Backend native frontend.
Presentation now lives in `frontend/`, with its own lockfile and build/image;
Backend contains no UI templates/assets. Existing CSS, native form routes, session
cookies, login URLs, key/vault/user controls and downloads remain available.
The native frontend remains permanent; Portal is separate. Shared Go operations
retain administrative permission rules independently of the presentation.

Compose groups `backend` and `frontend` in one project. The frontend forwards the
existing public routes and consumes a session-filtered dashboard data endpoint.
Public APIs, direct MCP, sync, data migrations, and their ownership rules retain
their behavior. Full JSON administration API work is recorded separately under point 3 below. Deployment,
independent frontend publication and migration boundaries are documented in the
[frontend guide](frontend-deployment.md). No installed deployment is migrated by
this source change.

### Point 2 validation results

- Backend: 69 top-level tests, 183 passing results including subtests/fuzz seeds,
  with the race detector; `go vet` passed.
- Frontend: Vite+ format/lint/type checks, six behavior tests and production build
  passed. React `19.3.1`, TypeScript `7.0.2` and Vite+ `1.4.0` are locked.
- Release tooling: 40 tests passed with the canonical temporary path described
  above. CI/release jobs now account for both components; frontend-only publication
  is a separate manual workflow and does not build/publish Backend.
- Both Dockerfiles built locally. Disposable-container checks passed for private
  session views, role/ownership isolation, exact int64 presentation, public origin
  and OAuth redirect, native forms, API/MCP forwarding, unbuffered sync SSE and
  frontend-only replacement on the same public port without recreating Backend
  or changing vault state. Executable healthchecks passed and proxy logs were
  checked to exclude both users' test API keys.
- Three Chromium workflows passed: anonymous/mobile login view; admin credentials,
  account changes, ZIP download, vault deletion/restoration, key rotation/logout;
  and the non-admin frontend. Desktop rendering was visually inspected.
- Production/development Compose configurations were resolved and checked for the
  two services, the preserved public port and backend-only storage/configuration.

These are local implementation checks. Real Google account login, the unnamed
external product's complete frontend flow, installed-data rollout, remote GitHub
CI and multi-architecture publication remain delivery validation in point 8.
The Google redirect and real Backend session/form behavior were exercised here;
the browser tests use sessions seeded only in a disposable, stopped database.
At the end of point 2, points 3–8 remained pending. No commit, push, image
publication or installed service migration was performed by the agent as part of point 2.


## Point 3 implementation

The owner authorized this point after confirming the separate frontend/backend
containers appear to work in Yard. The [administration API](administration-api.md)
now exposes authenticated account, personal-key, user/role/status, owned vault
lifecycle/ZIP and initial Yard/Gateway configuration operations. React mutations
use JSON APIs with same-origin session protection, visible errors and refreshed
state. Legacy browser forms/OAuth and the dependent-product entry points remain;
existing `/v1/*` and direct `/mcp` policies retain their own behavior.

Admin invariants also apply to user upserts and concurrent writes. Administrator
status never grants another account's vault data. Personal-key rotation invalidates
the old key without ending the browser session. Current roles/statuses are resolved
from Backend state for each administration request, without a new Google login.

Foundation addresses are validated by the released SDK origin normalizer and
persisted as configuration intent with conditional opaque revisions. No service
is contacted or marked connected; saved entries are `not_paired`. This configures
the foundation roles, not a limited product catalog. Points 4–7 will implement
the approved trust, registry/event, issuance and product execution mechanisms.

Migration 4 adds only the configuration table after validating the existing
baseline. Old histories still cannot trigger reset 3. Pre-upgrade data must be
retained for an older-binary rollback, because it rejects the new migration history.
Existing CI/release jobs already execute the changed Go/frontend/browser suites;
no new publication trigger or application version is required for this point.

### Point 3 validation results

- All 81 top-level Go tests passed with the race detector (201 passing results
  including subtests/fuzz seeds); `go vet` and Go formatting checks passed.
- Frontend type/lint/format checks, 11 React behavior tests and production build
  passed. Failure cases cover API refusal, unconfirmed network responses, a saved
  write with failed refresh, and stale connection revisions without silent replay.
- Both candidate Docker images built. The real split-deployment script passed,
  including preserved browser forms, API/MCP, session/role/ownership isolation,
  OAuth public redirect, exact revisions, sync SSE and frontend-only replacement.
- All three Chromium workflows passed. Admin flow now exercises JSON changes,
  visible errors, persistent foundation configuration, stale-tab conflict handling,
  removal, key rotation and logout. Member flow cannot view or call admin APIs.
- SQLite checks cover an actual supported baseline upgrade retaining users,
  personal keys, sessions, vaults and historical migration records; repeated
  startup; persisted connection revisions; concurrent changes; and protected
  administrator updates/upserts/deletes. Historical-reset refusal tests still pass.
- Dashboard rendering was visually inspected. No Compose/environment variable
  change is needed for this point. Existing CI/release jobs cover the new tests;
  remote GitHub execution, real Google exchange and the external product's full
  integration remain delivery validation in point 8.
No commit, push, tag, release or installed deployment change is performed here.
Points 4–8 remain pending; work stops after point 3 for owner approval.


### Owner-requested correction: Admin Manager account authority

The owner requested that the principal account be able to manage/delete promoted
admins. `NOX_BACKEND_ADMIN_EMAILS` now explicitly selects Admin Managers (including
the existing legacy fallback); creation order is not used. Managers may change the
status/role or delete ordinary admins. Ordinary admins cannot manage other admins.
Self/configured-manager accounts remain protected, including from upserts and
concurrent changes; deployment configuration controls the manager list.

This is server-derived account-management authority, not another stored role or
Suite delegation scope. Existing data ownership rules remain. No new database
migration, Compose change or environment variable is required. No commit, push or
publication was performed by this work. Both frontend and backend expose the
corresponding manager label and permissions. Point 4 remains pending.

Validation: all 86 top-level Go tests passed with the race detector (206 passing
results including subtests/fuzz seeds); `go vet`, Go formatting, frontend
format/type/lint checks, 14 React tests and the production frontend build passed.
Both Docker images built and all three real Chromium/split-deployment flows
passed, including promotion followed by manager disable/re-enable, demotion,
re-promotion and deletion of the secondary admin. API/storage tests verify
configured-manager/self protection, denial for ordinary admins, configuration
changes, legacy browser compatibility and access invalidation on deletion.
An older Backend missing capability fields keeps the frontend usable with account
management disabled and an update diagnostic; its behavior has a React test.


## Point 4 implementation

[Suite trust](suite-trust.md) implements the persistent Backend issuer, ES256
public/signing-key lifecycle, scoped technical credentials, HMAC pairing,
verified/pinned HTTPS declarations, encrypted restart/lost-response recovery,
acknowledgment, rotation/revocation, authorized installer deployment bindings and
conditional/idempotent state changes. It uses SDK 0.7.0 wire types/validators and
crypto helpers for any conforming product, without a hardcoded product catalog.

Migration 5 is additive in the same database/volume. Suite private material lives
in the separate encrypted file store under `/data/secrets/suite`; SQLite holds
verifiers/opaque handles. Missing secret-store material fails closed. Full `/data`
backup now includes both the database and those files. Clock safety, expiry and
current caller authority are checked independently of housekeeping.

The existing UI/client APIs retain their operation. New Suite secret-bearing
routes require approved HTTPS/proxy/network policy. Frontend rejects forged TLS
claims on its public HTTP listener and projects actual trust/recovery states.
A paired service is explicitly shown with registration pending. Native pairing
controls/delivery integration remain under point 8; the trust management APIs are
available independently of Portal. Durable trust invalidations commit with their
mutations; their public event delivery and the registry remain point 5.

CI/release Go tests now use the race detector; CI also runs full vet. No release,
version bump, push or installed deployment change is performed by this work.
Point 5 is not started; work stops after this point for owner approval.


### Point 4 validation results

- All 105 top-level Go tests passed with the race detector (241 passing results
  including subtests/fuzz seeds); full `go vet` and formatting checks passed.
- The published SDK verifies an actual delegation fixture signed by the persisted
  ES256 key. Real HTTPS tests cover valid pairing, wrong pins/identities/origins,
  redirects, current caller/grant enforcement, restart and lost-response recovery.
- Storage tests cover competing completions, conditional/idempotent writes,
  credential overlap/ack/expiry, full-generation service revocation, key rotation,
  erased recovery material, missing-master fail-closed behavior, clock rollback,
  authorized installer deployments and atomic rollback after an injected event
  write failure. Historical migration/data-preservation tests still pass.
- Frontend lint/types/format/build and 15 React tests passed. Both candidate images
  built; all three Chromium workflows and the existing split-deployment tests passed,
  including admin-manager controls, API/MCP, sync streaming and frontend replacement.
- The additional image-level TLS test passed through an actual HTTPS proxy with
  explicit proxy IP trust; forged/direct HTTP claims were rejected, issuer/keys
  survived a container restart, and direct Backend TLS plus its packaged HTTPS
  healthcheck passed. CI and release workflows run this test as a required check.
  The frontend-only publication workflow also validates its own startup, built
  dashboard/assets and rejection of forged TLS claims without building a Backend.
- Workflow/Compose YAML, local documentation links and diff whitespace were checked.
  No installed deployment, real service credential, Google account, GitHub release
  or remote workflow was changed/executed by these validations. Full actual-product
  rollout remains point 8. Point 5 remains pending.


## Point 5 implementation

[Registry/events operations](suite-registry.md) implements generic verified
registration, material/opaque revisions, current deployment authorization,
role-filtered consistent snapshots, durable conditional/idempotent mutations,
removal tombstones and association retirement. Every membership mutation commits
with its outcome and events in one transaction. Replays do not refetch products or
remove replacement registrations. No product-specific integration was introduced.

The persisted event feed supports bounded/scoped pages and 25-second long polling,
seven-day/100,000-event retention, resumable opaque cursors and explicit 410 snapshot
reconciliation. Normal restart preserves the generation. The offline
`reset-suite-event-cursors` operation forces reconciliation after full data restore.
Ordered, authorized observations use SDK uint64 string comparison, producer epochs,
clock checks and independent lifecycle/MCP freshness; they never delete membership.
The native connection status now distinguishes registered from paired/pending.

Migration 6 preserves the installed migration-5 authority, service credentials,
private store and all legacy account/vault data. The same existing CI Go test jobs
cover the added persistence and real HTTPS route tests; the mandatory image TLS
check also checks authentication on the registry/feed routes. Frontend-only build
and publication remain independent. No version bump, commit, push or release was
performed. Point 6 has not started.


### Point 5 validation results

- All 121 top-level Go tests passed with the race detector (257 passing results
  including subtests/fuzz seeds); full vet, formatting and native compilation passed.
- Real HTTPS service tests verify re-fetch/pinning, route/declaration identity,
  current authorization, role filtering, HTTP preconditions/validators, no-refetch
  lost-response recovery, empty 204 removal/replay and SDK snapshot/event reduction.
- Persistence tests cover competing writes, injected event-write rollback,
  removal/re-enrollment with delayed retries, cursor scoping/paging/retention,
  normal restart and explicit restore reconciliation. Migration 5-to-6 preserves
  installed issuer/key material and service credentials without inventing membership.
- Observations cover exact uint64 boundaries, duplicates/older reports/conflicting
  content, clock bounds, stale freshness, epoch reset/restart, reporting grants,
  deployment ownership and independent lifecycle/MCP state.
- The real default 25-second HTTPS poll passed. It originally exposed the inherited
  20-second socket deadline; the deadline now matches the feed's 30-second request
  budget. A second concurrent subscription is rejected and waiting leaves SQLite
  available to other operations. Cancellation/revocation wake and stop admission.
- Frontend format/types/lint, 15 React tests and production build passed. All 40
  release-tooling tests passed without invoking release creation. YAML, local
  documentation links, Python syntax and diff whitespace checks passed.
- The compiled native binary passed startup over actual TLS, its packaged healthcheck,
  authenticated empty registry/policy, migration 6, offline cursor reset and restart
  preservation of issuer/keyset/registry revision, using disposable data only.
- Both candidate Docker images built successfully. The split deployment and all
  three Chromium workflows passed, preserving dashboard/assets, cookies/roles,
  admin-manager operations, public OAuth redirects, vault int64 revisions, native
  forms, API/direct MCP, sync SSE and frontend-only image replacement. The matching
  Playwright Chromium was installed in temporary storage for this validation.
- The updated image-level HTTPS proxy check passed: the registry/event routes enforce
  authentication, forged direct HTTP transport claims are rejected, issuer/key state
  survives restart and the direct-TLS packaged healthcheck succeeds. The independent
  frontend image startup/assets/transport guard check also passed. Both Compose
  configurations validate with the Docker Compose CLI. Temporary networks/containers
  were removed by the test harnesses.
- Point 5 is complete and locally validated; work stops before point 6 for owner
  approval. No installed deployment, real product, remote GitHub workflow, tag or
  release was changed by these validations. This does not certify later Backend
  work or integrations with products that have not yet been adapted.


## Point 6 implementation

[Authorization issuance](suite-authorization.md) implements the contract's two
Gateway-authenticated exchange routes with actual ES256 user/probe profiles and
SDK-compatible responses. Current local account/key/role, source credential/grants,
registration, association, Suite capability and signing key determine authority.
Unknown client assertions cannot select identity, audience, scopes or lifetime.
Inspection maps to USER; ADMIN adds write and sensitive-read. No domain ownership
rule or native/direct MCP authentication model was changed.

Migration 7 preserves installed data and adds stable personal-key IDs, authorization
revisions and transactional native user/key invalidations, plus admission blocking
for a verified incompatible declaration update while retaining accepted membership.
The schema guard verifies the required invalidation triggers. Existing retention
and housekeeping handle events; no duplicate destructive cleanup trigger is added.
Fresh verified repair restores issuance; replay of an earlier outcome cannot repair
an unverified/incompatible endpoint. Exchanges persist no access tokens and do not
contact Google or refetch products on each renewal. No environment changes are needed.

At point-6 delivery, point 7 had not started. No version bump, commit, push, remote workflow or release
was performed by this work; work stops after point 6 for owner approval.


### Point 6 validation results

- All 137 top-level Go tests passed with the race detector (273 passing results,
  including subtests/fuzz seeds); full vet and Go formatting checks passed.
- Actual Backend-signed ES256 user/probe responses verify with the released SDK.
  Tests cover role mapping, response/claims agreement, unique request-local grants,
  wrong issuer/audience/actor/credential/registration/association, modified signature,
  profile separation and the five-second expiry safety margin. Readiness tokens
  cannot acquire user scopes or invoke tool/business methods under SDK admission.
- Issuance checks current prepared local accounts/keys without Google/product network
  calls, even after restart and with an offline target. Spoofed request assertions,
  query credentials, duplicate JSON, unacknowledged/wrong-purpose callers and stale
  registration revisions are rejected. Direct/native API keys remain independent.
- Revocation, source credential overlap/ACK, signing-key rotation/revocation, removed
  or replaced associations, unsupported capabilities and clock rollback are covered.
  The replaced-association case also passed its targeted race test after final review.
- Native account/key changes invalidate actual SDK cache entries through the HTTP
  event feed; a delayed renewal cannot repopulate invalidated authority. Injected
  event-write failures roll back role/status/key/deletion changes and metadata.
  Migration 6-to-7 preserves personal keys, sessions, issuer, registry and trust;
  normal restart preserves new authority IDs. Missing required triggers fail the
  installed-schema guard. Verified incompatible updates retain membership while
  blocking issuance until a fresh verified repair, including across restart.
- Both candidate Docker images built. All three Chromium/split-deployment flows
  passed, preserving native account-manager administration, cookies/roles, vaults,
  API/direct MCP, sync streaming, public OAuth redirects and frontend-only replacement.
  The mandatory image HTTPS test passed authentication and forged-HTTP rejection for
  both issuance routes, restart-persistent keys and packaged HTTPS healthchecks.
- Frontend checks and 15 React tests passed; all 40 release-tooling tests passed.
  Python syntax, documentation links and diff whitespace were checked. Existing CI
  and release jobs already run the added Go tests and updated mandatory image check.
- At that delivery, point 6 was complete locally and point 7 remained unstarted. No installed deployment,
  actual Gateway/product adaptation, remote workflow, commit, version bump, tag or
  release was performed by these validations. Native HTTP installs can perform their
  regression checks now; manual Suite exchanges require the later HTTPS setup.


## Point 7 implementation

[Backend as a Suite product](suite-product.md) adds the SDK-validated public
`/nox/integration` declaration and opt-in protected `/mcp/suite` endpoint. The
existing direct `/mcp` remains enabled by default and independently configurable.
Both share the existing ten tool implementations and owner-scoped storage.
Suite policies explicitly classify inspection, writes and sensitive diagnostics;
private catalogs filter by current verified capabilities. Probe tokens cannot
list tools or execute business methods.

The local authority adapter verifies current issuer/key, source Gateway/credential,
registration/association/admission, user/key/revision and scopes without HTTP calls
to itself or Google. User projection and trust snapshots are request-local. Shared
vault writes recheck authority inside their own write transaction, with cancellation
before mutation/commit; a token expiring after admission does not truncate an
already accepted operation. Execution capacity is shared across factories and stays
occupied until a timed-out executor actually exits.

Both Compose files expose the independent flags; the frontend protects the new
Suite path with the existing trusted HTTPS boundary. Required CI/release jobs already
run the added Go tests and updated image/proxy check. No schema migration is needed.
Installed HTTP deployments retain their existing frontend/API/direct MCP behavior;
protected Suite use still needs the later internal HTTPS setup and authorized
enrollment. Point 8 remains pending. No version bump, commit, push or release was
performed by this work.


### Point 7 validation results

- All 145 top-level Go tests passed with the race detector (291 passing results,
  including subtests/fuzz seeds). Full `go vet`, Go formatting and diff whitespace
  checks passed. Existing migration/data/legacy protocol tests remain included.
- Actual HTTPS Backend enrollment, official MCP client discovery/calls, USER/ADMIN
  policy, direct-MCP compatibility, owner-only vault lifecycle and concurrent user
  identity checks passed. Probe business/catalog methods and mixed batches are
  rejected; missing/expired controls and oversized inputs do not execute writes.
- Admission rejects revoked Gateway/account/personal/signing keys, removed own
  registration, incompatible target admission, wrong-target tokens and modified
  signatures. Shared mutation guards reject stale admitted authority and identity
  substitution. A separate storage test verifies grant expiry after valid admission
  without extending the execution deadline, while current revocations remain enforced.
- Independent SDK runtime factories share execution capacity. A deliberately
  non-cooperating test executor retains its slot after timeout until actual exit.
  All four direct/Suite endpoint flag combinations and environment aliases passed.
- Both final candidate images built. Split deployment and all three real Chromium
  flows passed, preserving native account/role administration, cookies, vaults,
  public origin/OAuth redirects, API/direct MCP, sync SSE and frontend replacement.
- The updated image HTTPS proxy test passed the real product declaration/Suite
  authentication boundary, forged HTTP rejection on both hops, persistent keys
  after restart and direct TLS/packaged healthcheck. The frontend-only image check
  independently passed its new Suite proxy guard without a Backend image.
- Frontend format/types/lint, all 15 React tests and its production build passed.
  All 40 release-tooling tests passed with `TMPDIR=/private/tmp` (canonical macOS
  fixture paths); no release was created. Both Compose configurations, Python
  syntax and local documentation links passed. Required existing CI/release jobs
  invoke the new Go tests and updated proxy script automatically.
- Work stops before point 8 for owner review. The installed deployment, other
  products, remote workflows, Git history, package version, tags and releases
  were not changed. Local validation is not a claim that the remaining Backend
  administration/delivery work or future Suite product adaptations are complete.


## Point 8 implementation

The [native Suite administration pane](suite-administration.md) completes the
initial foundation/product integration controls: generic pairing/proof handoff,
ACK by service-credential possession, conditional verified registration/update/
removal, credential generation rotation/revocation, installer deployment grants,
and issuer signing-key lifecycle. It uses the existing protected Suite routes and
their original SDK payloads; no approved wire rule, role mapping or product catalog
was changed. Gateway/Portal are not dependencies for local administration.

`GET /api/admin/suite` adds a current admin-only, bounded and consistently read
projection with no raw secret/verifier/private handle. Its transport/configuration
readiness distinguishes local trust management from approved outgoing enrollment.
Native account/vault controls survive a failed Suite status read. Lifecycle/MCP
observations, membership, pairing and credential recovery remain separate states.

Browser operations retain user-scoped non-secret operation/recovery metadata, so
an uncertain response can be reconciled or explicitly retried with the original
key/body/preconditions after reload. Proofs/credentials and setup responses stay in
memory; sensitive request fields must be supplied again to recover. Confirmed setup
responses also retain only their non-secret recovery receipt until forgotten.
ACK/expiry/revocation authority remains in Backend. There is no automatic mutation
retry, force update or fabricated successful integration.

CI's Docker readiness wait now handles connection resets in its bounded startup
loop. The shared packaged version check runs in both CI and Release, with tests
for transient reset, persistent failure and malformed response. The required HTTPS
check now includes a real Chromium administration flow using the actual images
and an explicit TLS protocol participant. Independent frontend publication retains
its standalone validation/build lifecycle. Release scripts resolve canonical paths
and symlink entry points, closing the recorded macOS portability issue.

The current database schema remains migration 7; no additional migration/env
variable is required for point 8. Docs now include the native operator workflow,
publication/installed checklist and the separate subsequent Gateway/product/CLI/
Portal work. No commit, version bump, push or release is performed by these changes.


### Point 8 validation results and completion

- All 148 top-level Go tests passed with `-race` (294 passing results including
  subtests/fuzz seeds). Full vet and Go formatting checks passed. New storage/HTTP
  tests cover current admin authority, safe consistent projections, live credential
  generations/deployment grants, restart preservation, transport readiness and
  native account/vault availability during an injected Suite dependency failure.
- Frontend format/type/lint and production build passed; all 26 React tests passed.
  They cover missing HTTPS, target-policy-independent local management, malformed/
  failed snapshots, stale revisions, unknown/204 responses, exact-operation replay
  after reload, user partitions and recovery without browser-stored secrets.
- Both final component images and the default-version Backend image built. All
  seven packaged version/alias/healthcheck scenarios passed using the shared CI/
  Release check, including its Docker health status and `/v1/info` assertions.
- All three native Chromium flows passed with the final images: anonymous/mobile
  entry, admin account/key/vault operations and non-admin isolation. Split image
  checks preserved public origin/OAuth redirects, cookies, legacy forms, exact
  revisions, API/direct MCP, sync SSE and frontend-only replacement.
- The additional actual HTTPS Chromium flow passed lost-response commit/reload/
  same-key recovery, SDK-compatible HMAC proof, pinned declaration completion,
  saved-provisioning ACK, verified registration, credential rotation/old-key denial,
  signing-key preparation/activation and 204 registry removal. Its small-screen
  check exposed long-label overflow; the corrected layout passed. A simulated Suite
  refresh failure kept native controls available. The participant is an explicit
  protocol fixture, not a claim that another real Suite product is adapted.
- The final TLS/proxy check passed rejected forged HTTP claims on both hops,
  persistent key state after intentional browser rotation and Backend restart,
  direct TLS and packaged healthchecks. The independent frontend-only startup/
  assets/proxy guard check passed without building/running Backend.
- All 41 release-tooling tests passed with the normal macOS temporary directory;
  canonical/symlink path handling no longer needs the earlier TMPDIR workaround.
  All three readiness unit tests passed, including the connection reset that broke
  the earlier GitHub run, bounded persistent failure and malformed application data.
- Both Compose configurations, workflow YAML, Python syntax, local documentation
  links and diff whitespace passed. Required CI and Release now run the packaged
  version matrix and native HTTPS browser administration; independent frontend
  publication remains separate. No dependencies or approved wire contracts changed.

All eight approved Backend implementation points are complete and locally
validated. The next actions are the [delivery checklist](suite-delivery.md): owner
commit/push, CI for the exact source, publication and installed verification.
Those remote/installed actions were not performed here. No real Google session,
other product, installed deployment/volume, Git history, package version or release
was modified by these local checks. Gateway, installer/product adaptations, CLI and
Portal remain their own subsequent implementations under the original contracts.


## Backend MCP publication correction — November 9, 2026

The own-product MCP adapter and CLI descriptors existed, but normal deployment
had no complete path to establish Backend's own product registration. Earlier
protocol acceptance explicitly prepared that registration; it did not prove
normal initial publication. This correction closes that specific gap.

With `NOX_BACKEND_SUITE_MCP_ENABLED=true`, opening the native dashboard as the
existing NoX Auth administrator automatically starts the administrator-protected
publication operation. It uses the unchanged pairing, HMAC, durable provisioning,
ACK, verified registration and transactional event operations. No privileged
caller, user-token forwarding, new account approval or credential-copying step is
introduced. The browser receives no service credential or pairing material.

The current association and workflow marker retain the actual administrative
initiator for restart reconciliation. Its account must remain active, administrative
and admitted by Auth. An interrupted acknowledged pairing can finish registration;
pre-ACK recovery retires only pending attempts marked by this workflow. Removed
or revoked associations/credentials are not automatically recreated. Unchanged
publication preserves identity and revision; declaration updates and the Suite
MCP switch update the advertised capability without changing direct MCP.

No schema migration, database reset, dependency change or additional environment
variable is required. Initial publication requires both updated Backend/frontend
images. The source-only implementation does not update installed images.

Verification completed locally on macOS ARM64 and disposable Linux containers:

- Full Backend `go test -race -mod=readonly ./... -count=1` and `go vet` passed,
  including fresh-registry publication, actual MCP/domain calls, admission/CSRF,
  concurrency, interruption, revoked-state protection and runtime shutdown/reopen.
- All 35 frontend tests and Vite+ format/lint/type checks passed. Backend and
  independent frontend Docker images built successfully.
- The required `check-suite-proxy.py --browser` path now rejects a pre-created
  Backend product record, opens the actual React dashboard in Chromium, verifies
  automatic publication and unchanged identity on reload, and consumes that
  publication through the unchanged published Gateway v0.0.1. Actual MCP execution,
  the ten canonical CLI definitions and restart persistence passed.
- Existing 41 release-tooling tests and three Python script tests passed. These
  tests use fixture repositories and do not publish a release.

The existing CI and release workflows already invoke this updated packaged check;
no workflow gate was removed. These are local results, not a claim that a new remote
CI run or the owner's installed acceptance has passed. Gateway, Yard and SDK source
were not changed. Work stops here for owner validation of this correction.
