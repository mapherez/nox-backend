# NoX Backend

[![CI](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/ci.yml)
[![CodeQL](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/mapherez/nox-backend/badge)](https://securityscorecards.dev/viewer/?uri=github.com/mapherez/nox-backend)
[![Docker](https://github.com/mapherez/nox-backend/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/mapherez/nox-backend/actions/workflows/docker-publish.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

Made with ❤️ by [Mapherez](https://github.com/mapherez). If you enjoy the project, please consider [buying me a beer 🍺](https://buymeacoffee.com/mapherez).

## What is NoX Backend?

NoX Backend is the self-hosted backend for [NoX Sync](https://github.com/mapherez/nox-sync), the Obsidian vault synchronization plugin. This repository contains the Go HTTP service, SQLite metadata, filesystem blob storage, web dashboard, and Docker configuration. The plugin source and releases belong to the separate NoX Sync repository.

The plugin communicates with the backend through its configured Server URL and API key. Neither repository needs the other's source code to build or run. Synchronization remains manually triggered by the plugin user.

## Features

- Google-authenticated `/vault-dashboard` with admin allowlist and user management.
- One reusable `noxsync_` API key per active user and multiple vaults per user.
- Vault download, soft delete, restore, permanent delete, and cloud storage size.
- HTTP + JSON API under `/v1`, with server-sent events for sync status.
- Per-vault sync locks, heartbeats, and stale-lock recovery.
- Manifest-based upload, download, delete, and conflict planning.
- SHA-256 validation, staged uploads, and atomic remote commits.
- Content-addressed blobs, current and previous file versions, and deletion tombstones.
- Read API for external integrations, including conditional downloads.

## Run the backend

For a **new installation**, download the production Compose file:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml -OutFile docker-compose.yml
```

Create a `.env` beside it:

```bash
NOX_SYNC_PUBLIC_URL=https://sync.example.com
NOX_SYNC_GOOGLE_CLIENT_ID=your-google-client-id
NOX_SYNC_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_SYNC_ADMIN_EMAILS=you@example.com
```

Set the Google OAuth authorized redirect URI to:

```text
https://sync.example.com/auth/google/callback
```

The production Compose file targets `ghcr.io/mapherez/nox-backend:latest`. It can be used after that image is published from this repository:

```bash
docker compose up -d
```

It maps host port `5710` to container port `8080`. Open `/vault-dashboard` at your public URL. For local testing, use `NOX_SYNC_PUBLIC_URL=http://localhost:5710` and the matching OAuth callback.

**Existing installations:** follow the [repository separation and update guide](docs/backend-separation.md). Update the image in your existing deployment; preserve its Compose project, volume, mounts, and configuration.

## Connect NoX Sync

Install the plugin using the instructions and releases in [mapherez/nox-sync](https://github.com/mapherez/nox-sync).

1. Sign in to this backend's dashboard with an allowlisted Google account.
2. Copy the Server URL and your API key.
3. Enter both in NoX Sync settings and use **Test connection**.
4. Select or create a backend vault, then manually sync.

An existing plugin can keep the same Server URL, API key, and selected vault after the repository separation. The backend routes, authentication, JSON responses, errors, and SSE behavior are unchanged.

## Persistent data and compatibility

All persistent state stays under `/data`:

- `/data/nox-sync.db`
- `/data/blobs`
- `/data/staging`
- `/data/logs`

Back up the complete directory as one consistent unit. Database metadata and blobs must stay together.

The separation does not change the database schema or add migrations. Existing migration files and startup behavior are retained. Before updating an existing installation, verify that its database has already applied the migrations included in this checkout; see the [update guide](docs/backend-separation.md).

For compatibility, runtime identifiers retain their existing names: `NOX_SYNC_*` variables, `noxsync_` keys, the `nox-sync` executable, Compose services and containers, and the `nox-sync-data` volume key. The Go module remains `github.com/mapherez/nox-sync/backend`; it is the local module identity, not a runtime dependency on the plugin repository.

## Build from source

```bash
cd backend
go test ./...
go run ./cmd/nox-sync
```

Configure environment variables before starting the backend. Use a separate data directory for development.

To build with Docker:

```bash
docker build -t nox-backend:dev ./backend
```

For local development with a `./data:/data` bind mount:

```bash
docker compose -f docker-compose.dev.yml up --build
```

The production image is published manually through the **Publish Docker image** GitHub Actions workflow. Publishing an image and updating a running server are separate operations. Backend version reporting continues to use `NOX_SYNC_VERSION` independently of plugin releases.

## Documentation

- [Backend setup and client connection](docs/user-setup.md)
- [Backend configuration](docs/backend-configuration.md)
- [Repository separation, validation, update, and rollback](docs/backend-separation.md)
- [Read API for Codex integration](docs/read-api.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Security policy](SECURITY.md)

CI runs Go formatting checks, backend tests, and Docker builds. CodeQL, Go vulnerability checks, fuzzing, and OpenSSF Scorecard workflows remain available.

## License

NoX Backend retains the original [GNU General Public License v3.0](LICENSE).