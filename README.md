# NoX Backend

[![CI](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml)
[![CodeQL](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/mapherez/nox-backend/badge)](https://securityscorecards.dev/viewer/?uri=github.com/mapherez/nox-backend)
[![Release](https://github.com/mapherez/nox-backend/actions/workflows/release.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/release.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

A self-hosted backend service providing storage, authentication, vault management and synchronization APIs for NoX applications and external integrations.

## Overview

NoX Backend is an independent Go service designed to be shared by multiple clients.

It provides a common backend for applications that need:

- authenticated users and API access;
- logical vaults containing files and revisions;
- persistent metadata and blob storage;
- file upload and download APIs;
- synchronization primitives;
- conflict-aware change planning;
- administrative tooling;
- self-hosted deployment through Docker.

Clients interact with NoX Backend through its HTTP API or native MCP endpoint and do not depend on its source code or release cycle.

[NoX Sync](https://github.com/mapherez/nox-sync) is one client of NoX Backend, using it to synchronize Obsidian vaults.

## Architecture

NoX Backend uses:

- **Go** for the HTTP service;
- **SQLite** for metadata;
- **filesystem blob storage** for file contents;
- **Google OAuth** for dashboard authentication;
- **API keys** for client authentication;
- **Docker** for deployment.

Persistent data is stored under:

```text
/data/
├── nox-backend.db
├── blobs/
├── staging/
└── logs/
```

The SQLite database and blob storage form a single logical data store and should be backed up together.

## Features

- HTTP + JSON API under `/v1`.
- Native MCP Streamable HTTP at `/mcp`, powered by [NoX MCP](https://github.com/mapherez/nox-mcp).
- Per-user API keys.
- Multiple vaults per user.
- Google-authenticated administration dashboard.
- User and admin management.
- Vault creation, listing, download, soft deletion, restoration and permanent deletion.
- File listing and downloads.
- Content-addressed blob storage.
- Current and previous file revisions.
- Deletion tombstones.
- SHA-256 content validation.
- Staged uploads and atomic commits.
- Manifest-based synchronization planning.
- Upload, download, delete and conflict operations.
- Per-vault synchronization locks.
- Heartbeats and stale-lock recovery.
- Server-sent events for synchronization status.
- Read APIs for external integrations.

The MCP endpoint runs in the same Go process, on the same HTTP server and port.
Its 10 tools share the existing backend logic and storage with the Stable Client
API. User operations use the existing per-user API key in
`Authorization: Bearer <API_KEY>`; health and service information are public.
Each tool exposes `_meta.cli` for a future NoX CLI. Binary file download remains
at `/v1/files/download`, and synchronization remains at `/v1/sync/*` through the
specialized HTTP API. See the [MCP contract](docs/mcp.md) for tools and connection
details.

## Docker

The production image is published to:

```text
ghcr.io/mapherez/nox-backend:latest
```

The repository includes a production `docker-compose.yml`.

For a new installation:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml -OutFile docker-compose.yml
```

Create a `.env` file beside it:

```env
NOX_BACKEND_PUBLIC_URL=https://backend.example.com
NOX_BACKEND_GOOGLE_CLIENT_ID=your-google-client-id
NOX_BACKEND_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_BACKEND_ADMIN_EMAILS=you@example.com
```

Start the service:

```bash
docker compose up -d
```

The default Compose configuration exposes:

```text
localhost:5710 → container:8080
```

The dashboard is available at:

```text
http://localhost:5710/vault-dashboard
```

or through the configured public URL.

Dashboard HTML, CSS and JavaScript live in `backend/internal/app/dashboard/` and
are embedded in the Go executable. Editing these files requires rebuilding the
backend image; no frontend build or external assets are required.

## Google OAuth

The web dashboard uses Google OAuth.

Configure the authorized redirect URI for your OAuth client:

```text
https://backend.example.com/auth/google/callback
```

For local testing:

```text
http://localhost:5710/auth/google/callback
```

`NOX_BACKEND_PUBLIC_URL` must match the origin used to access the backend.

Do not include a trailing slash.

## Client authentication

API clients connect using:

- the backend base URL;
- a user API key.

Example base URL:

```text
https://backend.example.com
```

Clients authenticate using:

```http
Authorization: Bearer <API_KEY>
```

API keys are created and managed through the backend dashboard.

## API

The HTTP API is exposed under:

```text
/v1
```

Current endpoint groups include:

```text
/v1/health
/v1/info
/v1/auth/*
/v1/vaults/*
/v1/files/*
/v1/status
/v1/sync/*
```

These cover:

- health and credential checks;
- vault management;
- file listing and downloads;
- synchronization sessions;
- locks and heartbeats;
- manifest planning;
- staged uploads;
- commits;
- synchronization status events.

The [stable client API](docs/client-api.md) covers service discovery, identity,
vault management, vault status and file reads. `/v1/sync/*` remains the
specialized synchronization protocol for synchronization clients.

## Example client: NoX Sync

[NoX Sync](https://github.com/mapherez/nox-sync) is an Obsidian plugin that uses NoX Backend as its remote synchronization service.

The plugin connects using the backend's:

```text
Server URL
API key
```

NoX Sync and NoX Backend are separate projects with independent repositories and release cycles.

NoX Backend is not specific to Obsidian and can be consumed by other applications through the same HTTP API.

## Persistent data

All persistent backend state lives under `/data`.

Back up the complete directory as one unit:

```text
/data
```

Do not restore only the SQLite database or only the blob directory independently. Metadata and file contents must remain consistent.

## Existing installations

NoX Backend was originally maintained inside the NoX Sync repository.

The repository separation does not intentionally change:

- database schema;
- stored data;
- API routes;
- JSON contracts;
- authentication behavior;
- API-key format;
- blob and staging paths;
- ports;
- stored schema and migration records.

Existing deployments should preserve their current Compose project, volumes, configuration and `/data` contents when switching to:

```text
ghcr.io/mapherez/nox-backend:latest
```

See:

[Repository separation and update guide](docs/backend-separation.md)

Do not use:

```bash
docker compose down -v
```

when the existing data must be preserved.

## Compatibility identifiers

New configuration uses `NOX_BACKEND_*`. Every runtime variable also accepts its
legacy `NOX_SYNC_*` name. A non-empty canonical value takes precedence; empty
values fall back to the legacy value and then to the default.

The legacy `nox-sync` executable remains available. Existing API keys (`noxsync_`),
`X-NoX-Sync-*` headers and the `nox_sync_session` cookie retain their names because
clients depend on them. References to the NoX Sync plugin identify a separate client.

New databases use `nox-backend.db`. An existing `nox-sync.db` is detected and used
without renaming it. If both active filenames exist, startup refuses to choose.
The optional `nox-backend migrate-database` command must run with the backend
stopped and after a complete backup; see the [update guide](docs/backend-separation.md).
The existing volume name does not need to change.

The Go module is `github.com/mapherez/nox-backend/backend`.

## Development

The backend source lives in:

```text
backend/
```

Run the test suite:

```bash
cd backend
go test ./...
```

Run the service locally:

```bash
go run ./cmd/nox-backend
```

Build the Docker image:

```bash
docker build -t nox-backend:dev ./backend
```

Run the development Compose configuration:

```bash
docker compose -f docker-compose.dev.yml up --build
```

The development configuration uses:

```text
./data:/data
```

as a local bind mount.

Do not use production data for development or testing.

## Releases

Backend releases are independent from client releases.

Release from a clean working tree on the branch you want to publish, with Node.js
24+, npm, Git, Go and a running Docker daemon available:

```bash
npm run release -- 0.1.0
```

`VERSION` contains the current version without `v` (initially `1.0.1`, matching the
latest historical release). Choose a different unused version for each release.
The example above illustrates the command syntax. An optional `v` prefix is
normalized; stable SemVer and prereleases such as `0.2.0-rc.1` are supported.
Build metadata (`+build`) is refused because Docker tags cannot contain `+`;
the complete tag must fit Docker's 128-character limit.

The command checks local and origin tags, updates `VERSION`, runs `go test ./...`
in `backend`, and builds the image with `VERSION=v0.1.0`. Failed checks restore
`VERSION` and create no commit, tag or push. Successful checks create
`chore: release v0.1.0`, an annotated `v0.1.0` tag, and push the current branch and
that tag atomically to origin. If the push fails, the local commit and tag are
kept and the command prints the exact recovery push command.

The **Release** GitHub Actions workflow runs only on `v*` tag pushes. It verifies
the tag against `VERSION`, tests Go, and publishes a multi-architecture image
(`linux/amd64`, `linux/arm64`). A stable release publishes exactly these image tags:

```text
ghcr.io/mapherez/nox-backend:v0.1.0
ghcr.io/mapherez/nox-backend:latest
```

Prereleases publish only their version tag, for example
`ghcr.io/mapherez/nox-backend:v1.1.0-rc.1`, and never change `latest`.

The image includes OCI labels `org.opencontainers.image.version`,
`org.opencontainers.image.revision`, `org.opencontainers.image.source` and
`org.opencontainers.image.created`. The creation timestamp is generated in UTC
immediately before the image build.
After publishing it, the workflow creates a GitHub Release with generated notes;
prereleases are marked as prerelease on GitHub. An existing release causes failure
and is never replaced. Monitor the workflow
until it completes: a successful local push starts the remote publication.

Release images report their Git tag through the existing `buildVersion` ldflags
mechanism in both `/v1/health` and `/v1/info`. A non-empty `NOX_BACKEND_VERSION`
overrides it at runtime; unversioned local builds report `dev`.

To deploy latest, set this in the deployment's `.env`:

```env
NOX_BACKEND_IMAGE_TAG=latest
```

To deploy a pinned release instead:

```env
NOX_BACKEND_IMAGE_TAG=v0.1.0
```

Then pull and apply the selected image:

```bash
docker compose pull
docker compose up -d
```

`latest` is the default when `NOX_BACKEND_IMAGE_TAG` is unset. Only stable releases
update `latest`; deploy a prerelease by explicitly pinning its version tag.
Publishing an image does not automatically update existing deployments.

Test release tooling without publishing anything:

```bash
node --test scripts/release.test.mjs
```

## Documentation

- [Setup and client connection](docs/user-setup.md)
- [Backend configuration](docs/backend-configuration.md)
- [Repository separation and updates](docs/backend-separation.md)
- [Stable client API](docs/client-api.md)
- [Read API](docs/read-api.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Security policy](SECURITY.md)

## Security

NoX Backend stores application data and manages authentication credentials.

For deployments exposed outside a trusted network:

- use HTTPS;
- restrict administrative access;
- protect OAuth credentials;
- treat API keys as secrets;
- back up `/data`;
- keep the backend image and host system updated.

Report security issues according to [SECURITY.md](SECURITY.md).

## License

NoX Backend is licensed under the [GNU General Public License v3.0](LICENSE).

Configuration compatibility: non-empty `NOX_BACKEND_*` values override matching
`NOX_SYNC_*` values. Legacy configurations continue to work. The physical database
name shown above is the new-installation default; existing `nox-sync.db` files are
used in place until an explicit offline migration.
