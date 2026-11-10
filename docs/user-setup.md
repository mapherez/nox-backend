# Install and connect to Backend

Backend provides a native dashboard, HTTP APIs and MCP tools for application
clients. This guide covers a current installation. For older deployments, also
read [updates and delivery](suite-delivery.md) before changing images or Compose.

## Prerequisites

- Docker with Docker Compose.
- A compatible NoX Auth service on the same Docker host, including its private
  `/v1/admission` endpoint. Auth v0.0.1 must be updated first.
- The Backend origin you will open in the browser. Private HTTP names/IPs work;
  no Backend certificate setup is required for local use.

Google credentials and allowed accounts are configured only in Auth. Its Compose
creates the shared runtime and restricted setup volumes used by Backend.

## Start Backend

Download the repository's `docker-compose.yml` and `.env.example` into a deployment
folder. Copy `.env.example` to `.env` and set:

```env
NOX_BACKEND_PUBLIC_URL=http://backend.home.arpa:5710
NOX_AUTH_PUBLIC_URL=https://auth.example.com
```

Use the exact browser origin, including a non-default port. If you use a custom
Auth/runtime volume name, set the matching name in Backend; default names require
no overrides. For existing data, preserve the actual data-volume name.
See [configuration](backend-configuration.md).

From that deployment folder:

```sh
docker compose pull
docker compose up -d
```

The `nox-backend` project contains the one-shot `auth-setup`, persistent `backend`
and independent `frontend` services. Setup enrolls the login callback automatically;
a successful setup exit is normal. Frontend owns port 5710 by default and forwards
to Backend on the internal port 8080. Data uses the `nox-backend-data` volume unless
the deployment config selects another name.

## Sign in and manage data

Open your origin plus `/vault-dashboard` and choose **NoX Login**. Select the
Google account admitted by Auth. Newly admitted accounts receive an ADMIN record;
existing identities, roles, keys and vault ownership are preserved.

Admitted ADMIN accounts using NoX Login can manage other admins. Self-deletion,
self-disable and self-role changes are forbidden. An admin does not own another
user's vaults. Backend administration does not add an account to Auth's allowlist.

The dashboard provides your Server URL and personal API key, owned vaults,
deleted-vault restore/purge, user administration and service connections.
Local logout affects Backend's browser session. **Sign out of all NoX apps**
revokes the current shared Auth session and its derived application sessions;
see [NoX Login](nox-login.md) for the five-minute enforcement bound and exclusions.

## Connect an application

Configure the application's Backend base URL without `/v1` and your personal key.
For new integrations, send the key in `Authorization: Bearer <API_KEY>`. Select or
create a vault using the [client API](client-api.md) or that application's UI.
Clients using the same key share access to that user's vaults; other users' data
remains isolated. Rotation invalidates the old key, so update each client using it.

File readers use the [read/download API](read-api.md); specialized sync clients use
`/v1/sync/*`. MCP clients can use the independent direct `/mcp` endpoint with its
[documented authentication](mcp.md). Client-specific installation and local UI
belong in each client's own documentation, such as the separate
[NoX Sync plugin](https://github.com/mapherez/nox-sync).

## Connect Suite services

Under **Service connections**, save Yard/Gateway native HTTP or HTTPS origins,
select **Verify**, then **Connect** and confirm the verified local identity. These
services must share the same-host runtime channel. No certificates or manual
credential files are required. Saving a URL alone does not register a service.
See [same-host setup](suite-local.md).

Backend's own tools are opt-in through `NOX_BACKEND_SUITE_MCP_ENABLED=true`.
With compatible Backend/frontend images installed, open the administrator dashboard
to publish them automatically. Refresh the Gateway/CLI catalog afterward. Direct
MCP remains independent. See [Backend Suite MCP](suite-product.md).

Use [troubleshooting](troubleshooting.md) for startup, login, missing tools or data
issues, and back up all Backend data before upgrades.
