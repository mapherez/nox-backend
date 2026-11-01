# Backend Configuration

NoX Backend is configured with environment variables and stores all persistent state under `/data` inside the container.

For an existing deployment, follow the [repository separation and update guide](backend-separation.md) before changing the image. Preserve the original deployment folder, Compose project, configuration, and data mount.

## Quick Docker Compose Setup

For a new installation, the easiest backend setup is (after the new image has been published):

1. Download `docker-compose.yml` from the repository.
2. Create a `.env` file next to it.
3. Run `docker compose up -d`.

Windows PowerShell:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml -OutFile docker-compose.yml
```

macOS or Linux:

```bash
curl -L https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml -o docker-compose.yml
```

Example `.env` file for a domain deployment:

```bash
NOX_BACKEND_PUBLIC_URL=https://sync.example.com
NOX_BACKEND_GOOGLE_CLIENT_ID=your-google-client-id
NOX_BACKEND_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_BACKEND_ADMIN_EMAILS=you@example.com
```

Example `.env` file for local testing:

```bash
NOX_BACKEND_PUBLIC_URL=http://localhost:5710
NOX_BACKEND_GOOGLE_CLIENT_ID=your-google-client-id
NOX_BACKEND_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_BACKEND_ADMIN_EMAILS=you@example.com
```

Start the backend:

```bash
docker compose up -d
```

Open:

```text
http://localhost:5710/vault-dashboard
```

## Docker Desktop Notes

If you use Docker Desktop, you can still use the same `docker-compose.yml` and `.env` files.

From a terminal in the folder containing both files:

```bash
docker compose up -d
```

Docker Desktop/Yard should show `backend` and `frontend` services in the same project. The backend container retains the name:

```text
nox-backend
```

The frontend owns host port `5710`; it forwards backend routes to the internal backend listener on `8080`:

```text
localhost:5710 -> frontend:8080 -> backend:8080
```

If you change the left side of the port mapping, also use that host port in your browser and plugin Server URL.

## Environment Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `NOX_BACKEND_ADDR` | `:8080` | HTTP listen address inside the container or process. |
| `NOX_BACKEND_DATA_DIR` | `/data` | Persistent backend data directory. |
| `NOX_BACKEND_VERSION` | embedded build version, or `dev` for unversioned local builds | Non-empty override of the version returned by both `/v1/health` and `/v1/info`. |
| `NOX_BACKEND_PUBLIC_URL` | request-derived URL | Public base URL used for dashboard display and Google OAuth callback URLs. Set this in production. The provided Compose files default it to `http://localhost:5710` for local use. |
| `NOX_BACKEND_GOOGLE_CLIENT_ID` | none | Google OAuth client ID for dashboard login. |
| `NOX_BACKEND_GOOGLE_CLIENT_SECRET` | none | Google OAuth client secret for dashboard login. |
| `NOX_BACKEND_ADMIN_EMAILS` | none | Comma-separated Admin Manager emails. Created/restored as active admins on startup; can manage other admins. Every listed account has manager authority and is protected from deletion/demotion/disable through the UI/API. |

## Google OAuth

Create a Google OAuth web client and add an authorized redirect URI that exactly matches your backend public URL plus `/auth/google/callback`.

For a public deployment:

```text
https://sync.example.com/auth/google/callback
```

For local testing:

```text
http://localhost:5710/auth/google/callback
```

Set `NOX_BACKEND_PUBLIC_URL` to the same origin, without a trailing slash:

```bash
NOX_BACKEND_PUBLIC_URL=https://sync.example.com
```

Dashboard users sign in with Google, but plugin sync still uses the per-user `noxsync_` API key from `/vault-dashboard`.

## Production Compose

The root `docker-compose.yml` is the minimal production-oriented example:

```bash
docker compose up -d
```

It targets the paired images published by this repository (use a release containing the frontend/backend separation):

```text
ghcr.io/mapherez/nox-backend:latest
ghcr.io/mapherez/nox-backend-frontend:latest
```

The frontend service exposes container port `8080` on host port `5710`:

```yaml
ports:
  - "5710:8080"
```

The new-installation example uses this Docker volume:

```text
nox-backend-data
```

The example explicitly names the volume, without a Compose project prefix. Existing deployments must keep their actual volume name using `NOX_BACKEND_DATA_VOLUME_NAME` or the legacy `NOX_SYNC_DATA_VOLUME_NAME`; see the [update guide](backend-separation.md).

Add the Google OAuth and admin email variables before exposing the dashboard publicly.

## Domain And Reverse Proxy Deployments

If you run NoX Backend behind a reverse proxy, the proxy should forward traffic to the backend container on port `8080`.

Example public URL:

```text
https://sync.example.com
```

Required matching settings:

```bash
NOX_BACKEND_PUBLIC_URL=https://sync.example.com
```

Google redirect URI:

```text
https://sync.example.com/auth/google/callback
```

Plugin Server URL:

```text
https://sync.example.com
```

The three values above should use the same scheme and host. A mismatch is the most common cause of Google login errors.

## Persistent Data

The backend creates and uses these paths under `NOX_BACKEND_DATA_DIR`:

| Path | Purpose |
| --- | --- |
| `/data/nox-backend.db` | SQLite database with users, sessions, API keys, vaults, file metadata, revisions, tombstones, locks, staged upload records, and conflict records. |
| `/data/blobs` | Finalized file contents stored by SHA-256 hash. |
| `/data/staging` | Temporary upload content for active sync sessions. |
| `/data/logs` | Reserved log directory. Docker console logs are still the primary runtime log target. |

Back up the whole `/data` directory as one unit. Restoring only the database or only the blobs can leave metadata and file content out of sync.

This release uses the multi-user, multi-vault schema. Existing databases must already have migrations 1, 2 and 3 with the expected schema. Older or unknown schemas are refused before startup migrations; the historical reset is never used as an automatic upgrade of existing data.

## Vault Delete, Restore, And Storage Usage

Deleting a vault from the dashboard or plugin is a soft delete:

- The vault disappears from the normal active vault list.
- Future sync and download attempts are blocked.
- The vault can still be restored.
- The vault can still occupy backend storage.

Permanently deleting a deleted vault removes its database metadata and makes it unrestorable. After metadata removal, NoX Backend also removes finalized blob files that are no longer referenced by any remaining vault metadata.

Because blobs are content-addressed by SHA-256 hash, a blob shared by another remaining vault is kept.

## Updating The Backend

For the initial switch from the old image, including validation and rollback, follow the [separation guide](backend-separation.md). Do not replace an existing Compose deployment with a fresh one or run `docker compose down -v`.

For subsequent updates to a Compose deployment that already uses `ghcr.io/mapherez/nox-backend:latest`:

```bash
docker compose pull
docker compose up -d
```

Check that the container restarted:

```bash
docker compose ps
```

The same Docker volume keeps `/data` between container updates. Preserve the deployment directory, Compose project name, mounts, environment, and credentials.

## Development Compose

For local source builds, use the development Compose file:

```bash
docker compose -f docker-compose.dev.yml up --build
```

This builds `./backend` locally and mounts `./data` to `/data` for easy inspection while developing.

## Local Docker Image Build

The version is resolved once at startup: a non-empty `NOX_BACKEND_VERSION` takes
precedence over the version embedded in the executable; an empty or unset
override uses the embedded version. With neither, an unversioned local build
reports `dev`. Both `/v1/health` and `/v1/info` report the same resolved value.

The publishing workflow embeds the exact Git tag of the checked-out commit,
or `git-<full commit SHA>` when there is no tag. Docker tags such as `latest`
are distribution labels and do not replace this backend version.

To build the backend image from a source checkout:

```bash
docker build -t nox-backend:dev ./backend
```

For a versioned local image:

```bash
docker build --build-arg VERSION=1.2.3 -t nox-backend:1.2.3 ./backend
```

For a versioned executable, run from `backend/`:

```bash
go build -ldflags "-X main.buildVersion=1.2.3" -o nox-backend ./cmd/nox-backend
```

See the [stable client API](client-api.md) for the health and metadata contracts.

For backend API-only development, you can run that local image directly (it does not serve the React dashboard):

```bash
docker run --rm --name nox-backend-dev -p 5710:8080 -v nox-backend-dev-data:/data nox-backend:dev
```

The production Compose example uses both `ghcr.io/mapherez/nox-backend:latest` and `ghcr.io/mapherez/nox-backend-frontend:latest`; both compatible images must be published before it can pull them. Use the development Compose example for the complete native frontend/backend from source. Building a local image does not publish anything to GitHub Container Registry. Running NoX Backend does not require external databases or external sync providers.

Configuration compatibility: non-empty `NOX_BACKEND_*` values override matching
`NOX_SYNC_*` values. Legacy configurations continue to work. The physical database
name shown above is the new-installation default; existing `nox-sync.db` files are
used in place until an explicit offline migration.

## Separate frontend configuration

The `frontend` service is part of the same `nox-backend` Compose project.
It owns the host port; the `backend` service owns data and credentials.

| Variable | Default | Purpose |
| --- | --- | --- |
| `NOX_FRONTEND_PORT` | `5710` | Host port forwarded to the frontend container's port 8080 |
| `NOX_FRONTEND_BACKEND_URL` | `http://backend:8080` | Internal Backend origin used by the frontend proxy; set it when changing the Backend listener |
| `NOX_FRONTEND_IMAGE_TAG` | Backend image tag, otherwise `latest` | Independently selected frontend image version or immutable `sha-<commit>` tag |

Keep `NOX_BACKEND_PUBLIC_URL` (or its legacy alias) equal to the external public
origin, including a changed host port. Register the same public origin's
`/auth/google/callback` with Google. Internal container addresses are not client
Server URLs or OAuth callback addresses. Public HTTPS may terminate at the
installation's existing trusted reverse proxy; the frontend preserves its
forwarded protocol. Read the [deployment guide](frontend-deployment.md).


## Initial foundation-service configuration

The native frontend's **Service connections** section and the
[administration APIs](administration-api.md) store approved Yard/Gateway HTTPS
origins in SQLite. These are administrator-managed settings, not additional
environment variables. Saved entries remain explicitly awaiting pairing until the
trust work is implemented. No service is contacted by saving its address.
Migration 4 adds the configuration table without resetting existing data. Retain
a pre-upgrade backup for rollback to a binary that predates migration 4.


## Suite trust configuration

[Suite trust and pairing](suite-trust.md) uses the same database/volume, plus
private encrypted files under `/data/secrets/suite`. Configure explicit approved
`NOX_BACKEND_SUITE_ALLOWED_CIDRS` and `NOX_BACKEND_SUITE_ALLOWED_PORTS` for product
verification. The default port is 443; an empty network policy permits no fetch.
These are deployment-wide policies for any product, not a fixed product list.

Suite management requires HTTPS `NOX_BACKEND_PUBLIC_URL`, with direct Backend TLS
(`NOX_BACKEND_TLS_CERT_FILE`, `NOX_BACKEND_TLS_KEY_FILE`, optional healthcheck
`NOX_BACKEND_TLS_CA_FILE`) or a trusted proxy boundary
(`NOX_BACKEND_SUITE_TRUSTED_PROXY_CIDRS` and
`NOX_FRONTEND_SUITE_TRUSTED_PROXY_CIDRS`). Proxy CIDRs default to empty. Existing
`NOX_SYNC_*` fallback rules apply to the new Backend variables. Certificate paths
must be mounted/readable by the process; they are not baked into an image.
See the trust guide before configuring a proxy: a supplied forwarding header or
private address is never sufficient on its own. These settings are optional for
legacy/native operation and necessary for the corresponding Suite deployment.

### Suite registry observations

`NOX_BACKEND_SUITE_REPORT_INTERVAL_MS` defaults to `15000` and
`NOX_BACKEND_SUITE_OBSERVATION_FRESHNESS_MS` defaults to `45000`. Both accept
positive integer milliseconds and their `NOX_SYNC_*` aliases. They configure
installation-wide reporting/freshness, not Google login or work-session duration.
See [registry/events operations](suite-registry.md) for snapshots and restore handling.

## Independent MCP access paths

`NOX_BACKEND_DIRECT_MCP_ENABLED=true` (default) preserves the existing direct
`/mcp` personal-key/public-tool policy. `NOX_BACKEND_SUITE_MCP_ENABLED=false`
(default) keeps protected Suite MCP off until HTTPS/trust/registration is ready.
Set it to `true` to mount `/mcp/suite` and advertise it in `GET /nox/integration`.
These flags are independent, accept only `true`/`false`, support the corresponding
`NOX_SYNC_*` aliases, and are forwarded by both Compose files. The declaration is
public metadata; it never grants trust or registration automatically.

See [Backend as a Suite product](suite-product.md) for enrollment, permissions,
request controls and validation. No public Internet exposure is required; internal
HTTPS is supported through the existing TLS/trusted-proxy configuration.


The native **Manage Suite integrations** pane shows the deployed readiness and
trust/registry state. `GET /api/admin/suite` is an authenticated non-secret read;
actual Suite mutations still require trusted HTTPS. Only target enrollment and
verified destination reads require the approved outgoing CIDRs/ports. Local key
management and revocation remain available independently of product reachability.
No point-8 environment variables or database migration are introduced. See the
[native guide](suite-administration.md) and [delivery checklist](suite-delivery.md).
