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

Docker Desktop should then show a container named:

```text
nox-backend
```

The container listens internally on port `8080`, but the Compose file exposes it on your machine as port `5710`:

```text
localhost:5710 -> container:8080
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
| `NOX_BACKEND_ADMIN_EMAILS` | none | Comma-separated bootstrap admin emails. These users are created or restored as active admins on startup. |

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

It targets the image published by this repository:

```text
ghcr.io/mapherez/nox-backend:latest
```

It exposes container port `8080` on host port `5710`:

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

You can then run that local image with:

```bash
docker run --rm --name nox-backend-dev -p 5710:8080 -v nox-backend-dev-data:/data nox-backend:dev
```

The `ghcr.io/mapherez/nox-backend:latest` tag is the image reference used by the production Compose example; it must be published before the example can pull it. Building a local image does not publish anything to GitHub Container Registry. Running NoX Backend does not require external databases or external sync providers.

Configuration compatibility: non-empty `NOX_BACKEND_*` values override matching
`NOX_SYNC_*` values. Legacy configurations continue to work. The physical database
name shown above is the new-installation default; existing `nox-sync.db` files are
used in place until an explicit offline migration.
