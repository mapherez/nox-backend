# NoX Backend

[![CI](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml)
[![CodeQL](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/mapherez/nox-backend/badge)](https://securityscorecards.dev/viewer/?uri=github.com/mapherez/nox-backend)
[![Docker](https://github.com/mapherez/nox-backend/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/docker-publish.yml)
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

Clients interact with NoX Backend exclusively through its HTTP API and do not depend on its source code or release cycle.

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
├── nox-sync.db
├── blobs/
├── staging/
└── logs/
```

The SQLite database and blob storage form a single logical data store and should be backed up together.

## Features

- HTTP + JSON API under `/v1`.
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
NOX_SYNC_PUBLIC_URL=https://backend.example.com
NOX_SYNC_GOOGLE_CLIENT_ID=your-google-client-id
NOX_SYNC_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_SYNC_ADMIN_EMAILS=you@example.com
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

`NOX_SYNC_PUBLIC_URL` must match the origin used to access the backend.

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
- storage paths;
- ports;
- migration behavior.

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

## Legacy identifiers

Some runtime identifiers still use the original `NOX_SYNC` naming for backwards compatibility:

```text
NOX_SYNC_*
noxsync_
nox-sync
nox-sync.db
nox-sync-data
```

The Go module also retains its existing identifier:

```text
github.com/mapherez/nox-sync/backend
```

These names are intentionally preserved to avoid unnecessary compatibility and data migration changes. They do not define the scope of NoX Backend.

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
go run ./cmd/nox-sync
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

The production image is published through the **Publish Docker image** GitHub Actions workflow.

Images report the exact Git tag of the built commit, or `git-<full commit SHA>`
when the commit has no tag. This version is separate from the Docker image tag
and is returned by both `/v1/health` and `/v1/info`. A non-empty
`NOX_SYNC_VERSION` overrides it at runtime; unversioned local builds report `dev`.

Publishing a new image does not automatically update existing deployments.

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
