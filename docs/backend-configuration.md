# Backend Configuration

NoX Backend is configured with environment variables and stores all persistent state under `/data` inside the container.

For an existing deployment, follow [updates and delivery](suite-delivery.md) before changing the image. The [repository separation guide](backend-separation.md) covers older naming/topology transitions. Preserve the original deployment folder, Compose project, configuration, and data mount.

## Quick Docker Compose Setup

For a new installation:

1. Run the compatible NoX Auth service on the same Docker host first; see [NoX Login](nox-login.md) for required service capability and update order.
2. Download `docker-compose.yml` from the repository.
3. Create a `.env` file next to it.
4. Run `docker compose up -d`.

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
NOX_BACKEND_PUBLIC_URL=https://backend.example.com
NOX_AUTH_PUBLIC_URL=https://auth.example.com
```

Example `.env` file for local testing:

```bash
NOX_BACKEND_PUBLIC_URL=http://localhost:5710
NOX_AUTH_PUBLIC_URL=https://auth.example.com
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

Docker Desktop/Yard shows `backend`, `frontend` and the one-shot `auth-setup` service in the same project. A successful setup exit is normal. The backend container retains the name:

```text
nox-backend
```

The frontend owns host port `5710`; it forwards backend routes to the internal backend listener on `8080`:

```text
localhost:5710 -> frontend:8080 -> backend:8080
```

If you change the left side of the port mapping, also use that host port in your browser and client base URL.

## Environment Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `NOX_BACKEND_ADDR` | `:8080` | HTTP listen address inside the container or process. |
| `NOX_BACKEND_DATA_DIR` | `/data` | Persistent backend data directory. |
| `NOX_BACKEND_VERSION` | embedded build version, or `dev` for unversioned local builds | Non-empty override of the version returned by both `/v1/health` and `/v1/info`. |
| `NOX_BACKEND_PUBLIC_URL` | unset outside Compose | Exact browser-facing HTTP(S) origin, including a non-default port. Required by Compose and NoX Login setup; registered automatically with Auth. |
| `NOX_AUTH_PUBLIC_URL` | none | HTTPS issuer of the shared Auth service. Required by Compose. Google credentials and allowed emails are configured there only. |

## NoX Login

[NoX Login integration](nox-login.md) documents setup, preserved identities/data,
sessions, account removal, independent API keys and logout. The one-shot setup
service registers the exact Backend `/auth/nox/callback` with Auth over its
restricted local socket. Only Auth's own callback is registered with Google.
Backend can use a private HTTP origin without certificates. Auth v0.0.1 must be
updated to a service release with `/v1/admission` before this adapter is deployed.

## Production Compose

The root `docker-compose.yml` is the minimal production-oriented example:

```bash
docker compose up -d
```

It uses compatible Backend/frontend images published by this repository:

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

Set the two required origins and use the same existing Auth volume names. The setup-only service receives the administrative volume; normal Backend runtime does not.

## Domain And Reverse Proxy Deployments

Point the reverse proxy at the frontend's published host port (5710 by default), or its port 8080 on a shared deployment network. Keep Backend private behind the frontend.

Example public URL:

```text
https://backend.example.com
```

Required matching settings:

```bash
NOX_BACKEND_PUBLIC_URL=https://backend.example.com
```

Auth automatically registers `NOX_BACKEND_PUBLIC_URL` plus `/auth/nox/callback`.
Open that same origin in the browser; existing clients continue using their
existing Server URL and API key. The public reverse proxy should target the
frontend container, which retains the browser/API proxy routes.

## Persistent Data

The backend creates and uses these paths under `NOX_BACKEND_DATA_DIR`:

| Path | Purpose |
| --- | --- |
| `/data/nox-backend.db` | SQLite database with users, sessions, API keys, vaults, file metadata, revisions, tombstones, locks, staged upload records, and conflict records. |
| `/data/nox-login.db` | Private NoX Login enrollment, identity bindings, pending transactions and sessions. Include it in backups. |
| `/data/blobs` | Finalized file contents stored by SHA-256 hash. |
| `/data/staging` | Temporary upload content for active sync sessions. |
| `/data/logs` | Reserved log directory. Docker console logs are still the primary runtime log target. |
| `/data/secrets/suite` | Encrypted Suite keys/recovery material and master key; back up with the database. |

Back up the whole `/data` directory as one unit. Restoring only the database or only the blobs can leave metadata and file content out of sync.

Backend uses the multi-user, multi-vault schema with additive Suite migrations. Existing databases must already have migrations 1, 2 and 3 with the expected schema. Older or unknown schemas are refused before startup migrations; the historical reset is never used as an automatic upgrade of existing data.

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

This builds `./backend` and `./frontend` independently and mounts disposable `./data` into Backend. The same-host Auth prerequisite and enrollment volumes also apply to development Compose.

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

The production Compose example uses both `ghcr.io/mapherez/nox-backend:latest` and `ghcr.io/mapherez/nox-backend-frontend:latest`; select compatible published versions for both services. Use the development Compose example for the complete native frontend/backend from source. Building a local image does not publish anything to GitHub Container Registry. Running NoX Backend does not require external databases or external sync providers.

Configuration compatibility: non-empty `NOX_BACKEND_*` values override matching
`NOX_SYNC_*` values. Existing non-authentication aliases remain supported; the old Backend Google/allowlist variables are replaced by NoX Auth configuration. The physical database
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
origin, including a changed host port. Auth setup registers that origin's
`/auth/nox/callback` automatically. Internal container addresses are not client
Server URLs or OAuth callback addresses. Public HTTPS may terminate at the
installation's existing trusted reverse proxy; the frontend preserves its
forwarded protocol. Read the [deployment guide](frontend-deployment.md).


## Initial foundation-service configuration

The native frontend's **Service connections** section stores Yard/Gateway HTTP
or HTTPS web addresses in SQLite. Use **Verify**, **Connect**, **Resume connection**
and **Cancel connection** to operate the local participant setup. Saving an address
alone does not establish trust. See [same-host setup](suite-local.md).

## Suite local channel

The supported Docker deployment mounts its local `nox-suite-local` volume at
`/run/nox-suite`. `NOX_BACKEND_SUITE_SOCKET_DIR` selects that directory; it is set
by the image and Compose. Bare binary execution leaves it disabled unless set
explicitly. Only participating services on that host share this channel. Native
URLs, OAuth and existing external-client APIs retain their own configuration.

The removed `NOX_BACKEND_SUITE_ALLOWED_CIDRS`, `NOX_BACKEND_SUITE_ALLOWED_PORTS`,
`NOX_BACKEND_SUITE_TRUSTED_PROXY_CIDRS` and frontend trusted-proxy variable are not
used. Their old aliases do not enable service networking. No certificate or proxy
allowlist setup is needed for the local channel. Existing native TLS certificate,
key and healthcheck CA options remain available for the web/client listener.

### Image and volume selection

| Variable | Default | Purpose |
| --- | --- | --- |
| `NOX_BACKEND_IMAGE_TAG` | `latest` | Backend image tag; also the frontend default unless overridden. |
| `NOX_BACKEND_DATA_VOLUME_NAME` | `nox-backend-data` | Actual data volume; preserve an existing installation's name. |
| `NOX_SUITE_LOCAL_VOLUME_NAME` | `nox-suite-local` | External runtime volume created by Auth, shared by same-host participants. |
| `NOX_AUTH_ADMIN_VOLUME_NAME` | `nox-auth-admin` | External Auth setup volume, mounted only by `auth-setup`. |

The production Compose uses external Auth/runtime volumes so a missing volume
fails rather than creating a disconnected installation. Auth creates them first.
See [`.env.example`](../.env.example) for supported deployment defaults.

### Suite registry observations

`NOX_BACKEND_SUITE_REPORT_INTERVAL_MS` defaults to `15000` and
`NOX_BACKEND_SUITE_OBSERVATION_FRESHNESS_MS` defaults to `45000`. Both accept
positive integer milliseconds and their existing environment aliases. They
configure reporting/freshness, not Google login or work-session duration.
See [registry/events operations](suite-registry.md).

## Independent MCP access paths

`NOX_BACKEND_DIRECT_MCP_ENABLED=true` preserves direct `/mcp` access under its
existing public/personal-key policy. `NOX_BACKEND_SUITE_MCP_ENABLED=false` keeps
Suite MCP opt-in. When enabled, `/mcp/suite` accepts Suite grants only through the
local channel. HTTPS and forwarded headers do not enable that path over TCP.
Both flags accept only `true`/`false` and retain their existing environment aliases.
When enabling Backend Suite MCP, install compatible Backend/frontend images and
open the administrator dashboard to initiate its automatic publication. A flag
or declaration alone does not establish membership. See [Suite product setup](suite-product.md).

**Manage Suite integrations** provides inspection and non-secret administration
through `/api/admin/suite` and `/api/admin/suite/operations/*`. Pairing, provisioning
and credential delivery are handled by local participants; the browser does not
copy or request service secrets. See [native administration](suite-administration.md).
