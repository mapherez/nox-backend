# Backend setup and client connection

This guide covers a new NoX Backend installation and connecting the [NoX Sync Obsidian plugin](https://github.com/mapherez/nox-sync). To update an existing backend with real data, use the [separation and update guide](backend-separation.md).

## What you need

- Docker Engine or Docker Desktop with Docker Compose.
- The shared NoX Auth service running on the same host, with the service release
  containing `/v1/admission`; Auth v0.0.1 must be updated first.
- A browser-facing Backend HTTP(S) origin. Private HTTP hostnames/IPs work.
- The client applications you already use with Backend.

Google credentials and allowed accounts live only in Auth. Backend setup registers
its callback automatically. See [NoX Login](nox-login.md) for update order and
preservation of existing users, keys and ownership.

## Start a new backend with Docker Compose

In a dedicated deployment folder, download this repository's Compose file.

Windows PowerShell:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml -OutFile docker-compose.yml
```

macOS or Linux:

```bash
curl -L https://raw.githubusercontent.com/mapherez/nox-backend/master/docker-compose.yml -o docker-compose.yml
```

Create `.env` beside it:

```bash
NOX_BACKEND_PUBLIC_URL=https://sync.example.com
NOX_AUTH_PUBLIC_URL=https://auth.example.com
```

For local testing, set `NOX_BACKEND_PUBLIC_URL=http://localhost:5710`.

After `ghcr.io/mapherez/nox-backend:latest` has been published, start the service:

```bash
docker compose up -d
```

For new split installations, `backend` and `frontend` are services of the `nox-backend` project. Host port `5710` maps to the frontend, which forwards API/auth/MCP requests to the internal Backend port `8080`. The frontend image must come from a release containing this separation; use the development Compose file while validating unreleased source. The default volume is `nox-backend-data`. Existing installations must preserve their actual data volume; both `NOX_BACKEND_DATA_VOLUME_NAME` and the legacy `NOX_SYNC_DATA_VOLUME_NAME` are supported.

## Open the dashboard

Open your configured origin plus `/vault-dashboard` and choose **NoX Login**.
Auth admits configured Google accounts. Existing Backend roles are preserved;
newly admitted accounts are ADMIN. Admitted ADMIN accounts signed in through
NoX Login have Admin Manager authority and can manage other admins, with
self-deletion/disable/role changes forbidden. Managing local Backend records does
not change Auth's allowlist.

## Initial Suite service addresses

Use **Service connections** to save Yard/Gateway web origins and perform the
existing local setup. Saving an address alone is not trust. Products communicate
through the same-host runtime volume; private HTTP origins need no certificates.
See [same-host setup](suite-local.md) and [administration APIs](administration-api.md).
The NoX Login adaptation does not authorize Yard to grant itself deployment rights.

## Connect the plugin

Plugin installation, releases, local builds, settings, conflict handling, and local trash instructions are maintained in [mapherez/nox-sync](https://github.com/mapherez/nox-sync). Plugin assets are available from its [releases](https://github.com/mapherez/nox-sync/releases).

In NoX Sync settings:

1. Paste the Server URL and API key from the backend dashboard.
2. Set a readable Client name, such as `Laptop`.
3. Use **Test connection**.
4. Select or create a backend vault.
5. Trigger manual sync.

Use the same public origin for `NOX_BACKEND_PUBLIC_URL`, the OAuth callback, and the plugin Server URL. Do not append `/v1` to the Server URL.

A second device can connect with the same user's API key, select the same vault, and manually sync to receive its files. Account admission is configured only in Auth; Backend admins manage local user records and roles. Each account retains its own key and vaults.

Rotating an API key invalidates the previous one. Repository separation does not require rotation or any changes to an existing plugin configuration.

## Build the backend locally

```bash
git clone https://github.com/mapherez/nox-backend.git
cd nox-backend
docker compose -f docker-compose.dev.yml up --build
```

The development configuration builds `./backend` and `./frontend` independently and mounts `./data` to `/data` only in the backend container. Use separate test data rather than a production data directory.

For Go development and all environment variables, see the [README](../README.md) and [backend configuration](backend-configuration.md).
Configuration compatibility: non-empty `NOX_BACKEND_*` values override matching
`NOX_SYNC_*` values. Non-authentication aliases remain supported; native Google credentials and account admission move to Auth. The physical database
name shown above is the new-installation default; existing `nox-sync.db` files are
used in place until an explicit offline migration.
