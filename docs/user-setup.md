# Backend setup and client connection

This guide covers a new NoX Backend installation and connecting the [NoX Sync Obsidian plugin](https://github.com/mapherez/nox-sync). To update an existing backend with real data, use the [separation and update guide](backend-separation.md).

## What you need

- Docker Engine or Docker Desktop with Docker Compose.
- A Google account and Google OAuth web client for dashboard login.
- A local or public URL for the backend.
- NoX Sync installed in Obsidian using the [plugin repository's instructions](https://github.com/mapherez/nox-sync).

The dashboard uses Google login. The plugin authenticates using the backend API key.

## Prepare Google login

Create a Google OAuth web client and register the redirect URI for your backend:

```text
https://sync.example.com/auth/google/callback
```

For local testing:

```text
http://localhost:5710/auth/google/callback
```

Keep the client ID and client secret for the backend configuration.

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
NOX_SYNC_PUBLIC_URL=https://sync.example.com
NOX_SYNC_GOOGLE_CLIENT_ID=your-google-client-id
NOX_SYNC_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_SYNC_ADMIN_EMAILS=you@example.com
```

For local testing, set `NOX_SYNC_PUBLIC_URL=http://localhost:5710`.

After `ghcr.io/mapherez/nox-backend:latest` has been published, start the service:

```bash
docker compose up -d
```

The service retains the name `nox-sync`. Host port `5710` maps to container port `8080`. The named volume key remains `nox-sync-data`; its actual Docker name depends on the Compose project.

## Open the dashboard

Open `https://sync.example.com/vault-dashboard`, or `http://localhost:5710/vault-dashboard` for local testing. Sign in with an admin email listed in `NOX_SYNC_ADMIN_EMAILS`.

The dashboard provides your Server URL and API key, vault management and downloads, and admin user management. Each user owns their own vaults and API key.

## Connect the plugin

Plugin installation, releases, local builds, settings, conflict handling, and local trash instructions are maintained in [mapherez/nox-sync](https://github.com/mapherez/nox-sync). Plugin assets are available from its [releases](https://github.com/mapherez/nox-sync/releases).

In NoX Sync settings:

1. Paste the Server URL and API key from the backend dashboard.
2. Set a readable Client name, such as `Laptop`.
3. Use **Test connection**.
4. Select or create a backend vault.
5. Trigger manual sync.

Use the same public origin for `NOX_SYNC_PUBLIC_URL`, the OAuth callback, and the plugin Server URL. Do not append `/v1` to the Server URL.

A second device can connect with the same user's API key, select the same vault, and manually sync to receive its files. Admin users can allowlist additional users from the dashboard; each user gets their own key and vaults.

Rotating an API key invalidates the previous one. Repository separation does not require rotation or any changes to an existing plugin configuration.

## Build the backend locally

```bash
git clone https://github.com/mapherez/nox-backend.git
cd nox-backend
docker compose -f docker-compose.dev.yml up --build
```

The development configuration builds `./backend` and mounts `./data` to `/data`. Use separate test data rather than a production data directory.

For Go development and all environment variables, see the [README](../README.md) and [backend configuration](backend-configuration.md).