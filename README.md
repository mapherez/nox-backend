# NoX Backend

[![CI](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml)
[![CodeQL](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/mapherez/nox-backend/badge)](https://securityscorecards.dev/viewer/?uri=github.com/mapherez/nox-backend)
[![Release](https://github.com/mapherez/nox-backend/actions/workflows/release.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/release.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

A self-hosted Go service for vault storage, file synchronization, client APIs and
NoX Suite integration authority. Its native React dashboard manages accounts,
personal API keys, owned vaults and Suite service connections.

## What Backend provides

- SQLite metadata and content-addressed file storage.
- Per-user vaults, committed file revisions, conditional downloads, soft deletion,
  restoration and permanent deletion of unreferenced data.
- Staged uploads, atomic sync commits, conflict planning, vault locks, heartbeats
  and synchronization-status events.
- HTTP APIs for application clients and external integrations.
- Ten MCP tools, available directly or through the Suite Gateway.
- Native user, role, key, vault and Suite administration.
- Suite pairing, service credentials, signing keys, product registration,
  authorization delegation, durable events and availability observations.

Backend is shared infrastructure for multiple independent applications. A client
uses its API contracts, not its source code or release cycle. It does not require
Gateway, Yard or Portal to serve its native APIs and dashboard. Dashboard login
uses the installation's shared NoX Auth service.

## Architecture

The `nox-backend` project contains three Compose services:

| Service | Responsibility |
| --- | --- |
| `auth-setup` | One-shot enrollment of Backend's login callback with NoX Auth; reuses persisted credentials. |
| `backend` | Go HTTP service, authorization, SQLite, blobs and the local Suite listener. |
| `frontend` | React + TypeScript + Vite+ dashboard, served by Nginx with same-origin forwarding to Backend. |

Frontend and Backend have separate images and build contexts in this repository.
A compatible frontend can be updated without rebuilding or replacing Backend.
The future NoX Portal is a separate application; it does not replace this native
frontend. See [frontend deployment](docs/frontend-deployment.md).

NoX Auth owns Google credentials and the allowed-account list. Backend owns its
local account roles/status, personal API keys, data ownership and Suite authority.
Yard manages deployments; Gateway routes MCP calls. Backend does not manage Docker
containers and does not mount the Docker socket.

## Install with Docker Compose

Run a compatible [NoX Auth](https://github.com/mapherez/nox-auth) service on the
same Docker host first. It must support the private `/v1/admission` endpoint;
Auth v0.0.1 does not. Its Compose creates the shared runtime and setup volumes.

Download `docker-compose.yml` and `.env.example` into a deployment folder, copy
`.env.example` to `.env`, and set the two required origins:

```env
NOX_BACKEND_PUBLIC_URL=http://backend.home.arpa:5710
NOX_AUTH_PUBLIC_URL=https://auth.example.com
```

Use the actual Backend address opened by your browser, including its port. Private
HTTP hostnames/IPs are supported. Backend does not need a certificate for local
use; Auth uses its own HTTPS origin for the Google login flow. Google client IDs,
secrets and allowed emails are configured only in Auth.

Start the project:

```sh
docker compose pull
docker compose up -d
```

Open your Backend origin plus `/vault-dashboard`. The default port mapping is:

```text
host:5710 → frontend:8080 → backend:8080
```

Published images:

```text
ghcr.io/mapherez/nox-backend:latest
ghcr.io/mapherez/nox-backend-frontend:latest
```

The production Compose automatically mounts the Auth runtime and setup volumes.
If their names were customized, use the matching names from that installation;
see [configuration](docs/backend-configuration.md) and [NoX Login](docs/nox-login.md).
For an existing installation, preserve its project, actual data volume, origin
and selected image tags. Follow the [update guide](docs/suite-delivery.md) rather
than replacing the deployment with a new-installation example.

## Login, accounts and client keys

Choose **NoX Login** in the dashboard. Auth authenticates the Google account and
checks its configured allowlist. Existing Backend identities, roles, keys and
vault ownership are retained. Newly admitted accounts receive an ADMIN record;
existing USER roles are preserved.

An admitted ADMIN using NoX Login has Admin Manager authority and can manage
other admins. Self-deletion, self-disable and self-role changes remain forbidden.
An ADMIN role does not grant access to another account's vaults. Backend user
administration does not modify Auth's allowed-account list.

Browser sessions last one year with renewal during admitted use. Admission is
rechecked within five minutes without routine Google login prompts. **Log out**
ends this application's session. **Sign out of all NoX apps** requests revocation
of the current Auth session and its derived application sessions, enforced within
five minutes. It does not revoke independent personal API keys or other devices'
Auth sessions. See [NoX Login](docs/nox-login.md) for the exact behavior.

API clients use the Backend base URL and their user's personal API key:

```http
Authorization: Bearer <API_KEY>
```

Keys are managed in the dashboard. Rotation invalidates the previous key; update
clients that use it. Keys do not grant access to other users' vaults. Account
admission and active status still apply. Do not put credentials in URLs or logs.

## HTTP and MCP interfaces

| Interface | Purpose |
| --- | --- |
| `/v1/health`, `/v1/info` | Service health and identity/version. |
| `/v1/auth/check`, `/v1/vaults/*`, `/v1/status` | Client identity, vault lifecycle and status. |
| `/v1/files`, `/v1/files/download` | Committed file metadata and binary downloads, with optional revision/hash conditions. |
| `/v1/sync/*` | Specialized synchronization protocol, uploads, planning, locks and events. |
| `/api/*` | Native dashboard/account/vault/admin APIs with their documented session or key requirements. |
| `/mcp` | Direct MCP, enabled by default; public discovery/health/info and eight authenticated tools. |
| `/nox/integration` | Public, non-secret Suite declaration. |
| `/api/nox/*`, `/mcp/suite` | Suite service protocol on the same-host Unix channel only; refused by the frontend/native TCP listener. |

The direct and Suite MCP adapters share the same ten canonical tools and domain
logic. Conventional `tools/list` omits `_meta.cli`. Canonical menu paths and rich
human-menu descriptors are available to NoX clients through the Suite adapter's
negotiated `nox/tools/definitions`. The direct `/mcp` endpoint does not expose that
extension. MCP tool names, arguments and permissions do not depend on menu layout.
Binary downloads and sync orchestration remain HTTP APIs, not additional MCP tools.
See the [MCP contract](docs/mcp.md).

## Connect to NoX Suite

Backend and participating Suite services run on the same Docker host. Their
Compose files share the local runtime volume; service traffic uses HTTP over Unix
sockets. It does not use public product URLs, LAN discovery, manually copied
certificates or per-installation CIDR allowlists. Browser/client URLs remain
separate and can be private HTTP or HTTPS origins without application prefixes.

In **Service connections**, save the Yard/Gateway native origin, select **Verify**,
then **Connect** and confirm the verified identity. Backend handles the local
pairing, persisted credentials, acknowledgment and registration. Use **Resume
connection** after an uncertain delivery. A saved address alone establishes no
trust; registered service status is separate from MCP availability.

To publish Backend's own tools through Gateway, set:

```env
NOX_BACKEND_SUITE_MCP_ENABLED=true
```

Update both Backend and frontend images and open the dashboard as the existing
administrator. The dashboard initiates Backend's publication automatically.
Restart recovery reuses the persisted association while its administrator remains
authorized; removed or revoked associations are not recreated automatically.
Direct `/mcp` remains independently controlled by `NOX_BACKEND_DIRECT_MCP_ENABLED`.
See [same-host setup](docs/suite-local.md) and [Backend as a Suite product](docs/suite-product.md).

## Persistent data and backups

Backend data resides under `/data` by default:

```text
/data/
├── nox-backend.db
├── nox-login.db
├── blobs/
├── staging/
├── logs/
└── secrets/
    └── suite/
```

An existing `nox-sync.db` remains in use instead of being renamed automatically.
SQLite sidecar files may also be present. Back up the complete directory
consistently, including NoX Login state and the Suite secret store. Database,
blobs and secrets must be restored together; a database-only backup cannot restore
service trust. Never run `docker compose down -v` when preserving installed data.

Supported `NOX_BACKEND_*` runtime settings retain their matching `NOX_SYNC_*`
aliases: a non-empty canonical value takes precedence, then the legacy value,
then the default. Former Backend Google credentials/allowlist settings no longer
configure login; those belong only to Auth. Existing `noxsync_` keys,
`X-NoX-Sync-*` headers and the `nox_sync_session` cookie retain their protocol names.
These identifiers do not make Backend dependent on the separate NoX Sync plugin.

## Development

Go service source lives in `backend/`; the React source lives in `frontend/`.
Use disposable data for development. The complete source deployment uses the
same Auth prerequisite and `.env` origins as production:

```sh
docker compose -f docker-compose.dev.yml up --build
```

Backend tests:

```sh
cd backend
go test ./...
```

Frontend checks and build, from the repository root:

```sh
npm ci --prefix frontend
npm run check --prefix frontend
npm test --prefix frontend
npm run build --prefix frontend
```

See [frontend development](docs/frontend-deployment.md) for the development proxy,
independent image updates and disposable browser/integration checks. Container
health uses `nox-backend healthcheck` against `/v1/health`; it checks HTTP response
after initialization, not ongoing database/disk integrity or complete Suite readiness.

## Releases and updates

From a clean Git checkout, with Node.js 24+, npm, Git, Go and Docker available:

```sh
npm run release
```

Enter an unused SemVer version, or pass it as `npm run release -- X.Y.Z`.
The script checks the version/tags, validates Go and frontend code, builds both
images, commits the version and pushes the branch/tag atomically. Failed local
checks restore the package version. A successful push starts GitHub Actions;
monitor that workflow for image publication and the GitHub Release result.

Stable releases publish the versioned tags and update `latest` for both images.
Prereleases publish only their versioned tags. Build metadata (`+build`) is not
accepted because Docker tags cannot contain `+`.

For normal deployment updates, back up complete data first, preserve the existing
Compose project/volumes, select `NOX_BACKEND_IMAGE_TAG` in `.env`, then run:

```sh
docker compose pull
docker compose up -d
```

Frontend inherits that tag unless `NOX_FRONTEND_IMAGE_TAG` selects a compatible
independent frontend image. Changes to the shipped Compose, Auth requirements or
mounts must also be applied when upgrading from an older topology; pulling images
alone does not add services or mounts. See [delivery and updates](docs/suite-delivery.md).

## Documentation

| Topic | Guide |
| --- | --- |
| Installation and use | [Setup](docs/user-setup.md), [configuration](docs/backend-configuration.md), [troubleshooting](docs/troubleshooting.md) |
| Login and account admission | [NoX Login](docs/nox-login.md) |
| Client contracts | [Stable Client API](docs/client-api.md), [read/download API](docs/read-api.md), [MCP](docs/mcp.md) |
| Native management | [Administration API](docs/administration-api.md) |
| Suite setup and management | [Same-host channel](docs/suite-local.md), [native Suite administration](docs/suite-administration.md) |
| Suite protocol | [Trust](docs/suite-trust.md), [registry/events](docs/suite-registry.md), [delegation](docs/suite-authorization.md), [Backend product adapter](docs/suite-product.md) |
| Build and delivery | [Frontend](docs/frontend-deployment.md), [updates and validation](docs/suite-delivery.md) |
| Historical context | [Repository separation](docs/backend-separation.md), [implementation record](docs/suite-migration.md); these are not fresh-install instructions. |

## Security and license

Keep personal keys, cookies, Google credentials, Auth setup credentials and Suite
secret storage private. Use HTTPS and appropriate access restrictions for native
services exposed outside a trusted network. Protect the host and its shared local
volumes, and back up complete data consistently. Report security issues according
to [SECURITY.md](SECURITY.md).

NoX Backend is licensed under the [GNU General Public License v3.0](LICENSE).
