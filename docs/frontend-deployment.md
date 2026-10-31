# Native frontend and backend deployment

The native frontend uses React + TypeScript + Vite+. The backend remains Go.
Both belong to this repository and to one `nox-backend` Compose/Yard project,
with `frontend` and `backend` services and independent build contexts/images.
The future Portal is a separate application; it does not replace this frontend.

## Runtime boundary

The public port remains 5710 by default. The frontend serves `/vault-dashboard`
and its assets. It forwards `/auth/*`, `/api/*`, `/v1/*`, native dashboard form
and ZIP-download routes, `/mcp`, `/mcp/suite`, `/nox/integration`, and other
existing backend routes. The protected [Suite MCP path](suite-product.md) uses the
explicit trusted HTTPS boundary, independently of direct MCP. It preserves
the public Host, forwarded protocol, cookies, authorization headers, request
bodies, redirects and streaming responses. HTTPS Backend targets use certificate verification with the image CA bundle;
install the deployment CA bundle when using a private CA. Forwarded requests
are not automatically retried. The backend listener stays internal
in the supplied Compose files. Keep the existing external origin, proxy routing
and Google callback URL when migrating; no second public application origin or
browser cross-origin credential flow is introduced.

Backend owns all session checks, account roles, key/vault/user operations and
SQLite/blob access. `GET /api/dashboard` returns only the current session's view,
with public login/configuration data when anonymous. It uses `Cache-Control:
no-store`; the frontend bundle contains no user data. Non-admins do not receive
the administrative user list. API Bearer keys do not authenticate this browser
view. Only required presentation fields are projected; Google subjects are absent.
The [administration JSON API](administration-api.md) supplies account/role,
personal-key, vault and foundation-configuration operations. React uses those
APIs for changes; existing browser form and authentication routes remain usable.
Initial Yard/Gateway addresses are persistent admin configuration. Their status
reflects [Suite pairing/credential state](suite-trust.md), distinguishing pending
pairing, acknowledged trust with registration pending, and required recovery.
The frontend does not report an unverified address as a successful integration.

The frontend proxy has no response/request buffering for streamed API responses,
including the existing sync SSE endpoint. Its idle read/send timeout is one day;
this is a transport inactivity setting, not a login/session or delegation timer.
Docker DNS is refreshed for replacement Backend containers. Access logs omit
query strings and credentials; routine upstream error logging is suppressed to
avoid logging legacy API-key query parameters. Backend failures still produce
HTTP errors and a visible retry state in React. `/healthz` reports frontend HTTP
readiness; the existing `/v1/health` and Backend executable healthcheck retain
their meanings. These checks do not claim full Suite MCP readiness.

## Build and run from source

Before a release containing this split is published, use source builds. The old
published Backend `v1.0.7` image does not supply `/api/dashboard`; do not combine
that old image with the new frontend.

```sh
docker compose -f docker-compose.dev.yml up --build
```

Development data is mounted from `./data` only into Backend. Do not point this
at installed application data while validating the change. In Yard the project
contains the two services; they are not separate registered Suite products.
Compose does not itself register anything with the Suite.

Build only the frontend after a UI change:

```sh
docker compose -f docker-compose.dev.yml build frontend
docker compose -f docker-compose.dev.yml up -d --no-deps frontend
```

The second command replaces only the frontend service; Backend and its data stay
in place. API compatibility between the selected images remains required.
Use the existing Compose project/options when applying this to a prepared split
installation. Development and production examples use different default project
names; an explicit `-p` or `COMPOSE_PROJECT_NAME` retains the installation's name.

## Published images and independent frontend updates

A normal application release builds/tests both components and publishes:

- `ghcr.io/mapherez/nox-backend:<version>`
- `ghcr.io/mapherez/nox-backend-frontend:<version>`

Stable releases also update each image's `latest` tag; prereleases do not.
The release tooling validates React types/lint/tests and builds both images.
CI additionally runs the real split-deployment and browser checks.

The **Publish frontend image** workflow is manual. It checks and builds only
frontend source, publishes the immutable `sha-<full-commit>` tag and does not
build/publish Backend, alter the application version, update `latest`, or create
an application release. It refuses an existing tag or an ambiguous registry
lookup failure. To use a separately published compatible UI, set:

```env
NOX_FRONTEND_IMAGE_TAG=sha-<full-commit>
```

Then update only that service with the existing project/options:

```sh
docker compose pull frontend
docker compose up -d --no-deps frontend
```

No workflow publication is performed by local validation of point 2.

## Existing installations

This is a deployment topology change, not a database migration. First preserve
the actual Compose project name, public URL, ports, environment, data mount and
image references. Follow the [storage/update guide](backend-separation.md) for
backups and validation on a copy. Existing `NOX_BACKEND_*`/`NOX_SYNC_*` precedence,
legacy keys/cookies/headers, database names and migration history remain valid.

The old Compose service was `nox-backend`; the new services are `backend` and
`frontend`. The backend container name remains `nox-backend` in the production
example. Stop and remove the old service container using the old Compose file
before replacing the configuration, otherwise the old name/port can conflict.
Remove only that stopped container; retain the existing volume and project.
Do not run `down -v`. Configure the new backend service to use the exact existing
`/data` source and the frontend to own the same external port. Keep any existing
custom service/container names required by deployment automation explicitly in
the installation's configuration, and update its routing deliberately.

Use a matched candidate pair containing this separation. Validate health, login,
existing keys and sessions, roles, vault IDs/revisions, downloads, sync, wiki/read
integration and the product that depends on the native frontend before changing
the installed deployment. Actual production migration/delivery remains point 8.
Rollback requires restoring the previous image/topology and the same preserved
data mount. Point 2 added no schema change. Point 3 adds the non-destructive
connection-configuration table through migration 4. See the
[administration migration notes](administration-api.md#persistence-and-delivery)
before upgrading: older binaries require the pre-upgrade data backup for rollback.

## Verification

From the repository root, with the Go/Node toolchains available:

```sh
npm ci --prefix frontend
npm run check --prefix frontend
npm test --prefix frontend
npm run build --prefix frontend
docker build --build-arg VERSION=step2-validation -t nox-backend:step2-validation ./backend
docker build -t nox-backend-frontend:step2-validation ./frontend
python3 scripts/check-split-deployment.py
```

The integration script creates its own network, temporary data and containers,
checks browser/API/MCP routing, user isolation, streaming and frontend replacement,
and removes its containers/network afterwards. It seeds sessions only in that
disposable database. It never attaches existing application volumes. To include
real Chromium flows, install it with `npm exec --prefix frontend -- playwright
install chromium` and pass `--browser` to the integration script.


Suite trust deployment adds optional HTTPS/approved-network settings and a
frontend trusted-ingress CIDR allowlist. The native HTTP flows are preserved;
Suite credential/proof traffic requires a trusted HTTPS boundary. See the
[trust deployment guide](suite-trust.md#tls-deployment-choices). Migration 5 also
requires backing up the complete secret store with SQLite for rollback/recovery.
