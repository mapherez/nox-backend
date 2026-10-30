# NoX Suite migration status and compatibility baseline

Updated **October 30, 2026**. Backend baseline: commit `a2382ec` (`v1.0.7`).
The SDK dependency is now `github.com/mapherez/nox-mcp v0.7.0`; the Backend
application version remains `1.0.7`. Suite wire compatibility remains `1.0`.

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
| 1 | Record the existing baseline and consume SDK `0.7.0` | Implemented and locally validated; awaiting owner approval to proceed |
| 2 | Separate frontend/backend builds, images, containers, and business operations within the same project | Pending |
| 3 | Complete administration APIs and retain native frontend functionality | Pending |
| 4 | Implement persistent service trust and pairing | Pending |
| 5 | Implement registry and durable events | Pending |
| 6 | Implement authorization issuance | Pending |
| 7 | Adapt Backend as a Suite product | Pending |
| 8 | Complete administration, deployment, documentation, CI, and delivery validation | Pending |

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

Release script portability remains recorded for point 8; no release script or
workflow was changed in point 1. These local results do not claim GitHub CI
approval, multi-architecture image validation, or completed Suite service flows.
Pairing, registry, issuance, Suite MCP, and frontend separation are still pending
in their respective numbered points. No application release was created.
