# Troubleshooting

This guide covers Backend and its native frontend. For a particular client's
installation, local UI or retry behavior, use that client's documentation.
Backend HTTP contracts are documented in the [Stable Client API](client-api.md)
and [file read API](read-api.md).

## Check service startup and routing

Run from the existing deployment folder with its normal Compose project/options:

```sh
docker compose ps
docker compose logs --tail=80 auth-setup backend frontend
```

`auth-setup` is a one-shot service: a successful exit is normal. Backend starts
after setup succeeds; frontend starts after Backend's healthcheck passes.
Use Compose service names above, not the container name, with `compose logs`.
Do not post credentials, cookies or resolved secret-bearing configuration in logs
or support requests.

The default public mapping is `host:5710 → frontend:8080 → backend:8080`.
Open the configured origin plus `/vault-dashboard`. If the port is occupied,
set `NOX_FRONTEND_PORT` and update `NOX_BACKEND_PUBLIC_URL` to the matching
browser origin, including its port, then apply the Compose setup again.
A reverse proxy should target the frontend, not the private Backend service.

Frontend `/healthz` checks its HTTP service. Backend `/v1/health` checks HTTP
responsiveness after initialization. Neither alone proves login, ongoing disk
integrity or full Suite MCP availability.

## NoX Login is unavailable or setup exits

Run a compatible Auth service on the same Docker host first. Backend requires
Auth's private `/v1/admission`; Auth v0.0.1 lacks it. Check
`NOX_BACKEND_PUBLIC_URL` and `NOX_AUTH_PUBLIC_URL`, then inspect the setup log.
Pull the selected images before recreating services when a cached image is old:

```sh
docker compose pull
docker compose up -d
```

The runtime and restricted setup volumes must match Auth's actual volume names.
Only `auth-setup` receives Auth's admin volume; do not add it to Backend runtime
or frontend. The one-shot command is `auth-setup` under the image ENTRYPOINT.
Private HTTP Backend origins need no certificates. See [NoX Login](nox-login.md).

## Login returns an error or access is denied

Open the exact `NOX_BACKEND_PUBLIC_URL`. The configured Auth issuer must match
the persisted enrollment. Re-run setup when changing the Backend browser origin.
Only Auth's callback belongs in Google configuration; Backend's callback is
registered automatically with Auth.

The account must be allowed by Auth and active in Backend. Adding a Backend user
record does not add its email to Auth's allowlist. Conflicting identity bindings
are refused rather than reassigned. Previous native-Google sessions require one
new NoX Login; existing API keys do not need rotation for that transition.

## Cannot delete another admin

An admitted ADMIN signed in through NoX Login has Admin Manager authority and can
manage other admins. Self-deletion, self-disable and self-role changes remain
forbidden. Existing USER roles are retained. Google credentials and the allowed
email list belong only to Auth; old Backend email-list variables do not grant
admission. Local roles and vault ownership remain Backend responsibilities.

## API key rejected

Missing credentials return `AUTH_REQUIRED`; invalid or rotated keys and disabled
accounts return `AUTH_FAILED`. Compare the client's configured key with the current
key in Backend. Rotating a key requires replacing it in every client using it.

Keys belong to individual users. Even an ADMIN key cannot read another user's
vault. Central Auth account admission also applies to key requests, with positive
admission reused for at most five minutes. Expired admission cannot be extended
while Auth is unavailable. See [authentication](nox-login.md).

## Client cannot reach Backend

Check the client's base URL, protocol, port and network access. Do not append
`/v1` to the configured base URL. Verify container health and the frontend mapping;
use the actual host address from other computers, not their own `localhost`.
For a configured domain or reverse proxy, use the intended Backend origin.
A client's `SERVER_UNREACHABLE` label can represent DNS, routing, proxy or service
failure; it does not by itself identify a Backend authentication problem.

## Yard/Gateway address saved but not registered

Saving an address only stores configuration. Use **Verify**, then **Connect** in
**Service connections**. The participant must support the shared protocol, run
on the same host and mount the same local runtime volume. Remote LAN addresses
or HTTPS do not establish same-host membership.

Use **Resume connection** for uncertain delivery or inspect **Manage Suite
integrations** for recovery/revocation state. Do not copy pairing secrets into
the browser or remove data to force setup. See [same-host setup](suite-local.md).

## Backend tools missing from Gateway or CLI

Enable `NOX_BACKEND_SUITE_MCP_ENABLED=true`, update both Backend and frontend
images, and open the dashboard as the existing administrator. The dashboard
publishes Backend's own tools. A visible publication error has an explicit retry
action. Refresh the Gateway client's catalog after publication.

`/nox/integration` is a declaration, not proof of registration. `/mcp/suite` is
local-only: a 403 over the frontend/native TCP port is expected and is not fixed
by adding HTTPS. The normal Gateway client endpoint is Gateway's `/mcp` with the
user's Backend key. Removed/revoked Backend associations need explicit recovery;
the dashboard does not silently recreate them. See [Suite product setup](suite-product.md).

## Vault missing or synchronization blocked

Confirm the client selected an active vault owned by its key's user. Restore a
soft-deleted vault in the dashboard or select another active vault. A permanently
deleted vault cannot be restored.

Only one sync session owns a particular vault's lock at a time. Different vaults
can synchronize concurrently. After interrupted heartbeats, stale-lock expiry
allows recovery and cleans abandoned staging content. Clients should surface
conflicts from the sync plan and let the user resolve them, rather than silently
replace changed files. Client-specific labels such as `BLOCKED_REMOTE` or
`CONFLICT` are not independent Backend login states.

Preserve vault data before manual recovery. Do not delete lock/schema records,
reset databases or repeatedly replay mutations with an unknown outcome.

## Deleted vault still uses storage

Deletion is initially soft so the vault remains restorable. Permanently purge it
to remove its metadata and reclaim finalized blobs that no remaining metadata
references. Shared content-addressed blobs remain while another vault/revision
still uses them.

## Vaults disappeared after an update

Stop and verify the actual `/data` mount before making changes. The production
example explicitly names its default data volume `nox-backend-data`; an older or
custom deployment may use a different name. Preserve that exact name through
`NOX_BACKEND_DATA_VOLUME_NAME` or its supported legacy alias. Do not assume a
Compose project prefix or change the volume to match a new-installation example.

Back up and restore the complete data directory, including blobs, SQLite sidecars,
`nox-login.db` and `secrets/suite`. Do not run `docker compose down -v`. Unknown
schema histories or mismatched secret storage must be investigated, not bypassed
by deleting migration records. See [updates and backups](suite-delivery.md).
