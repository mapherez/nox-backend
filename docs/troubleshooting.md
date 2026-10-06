# Troubleshooting

NoX Backend surfaces explicit states instead of silently continuing when sync is unsafe.

## Plugin installation and local behavior

Plugin installation, local builds, conflict resolution UI, and local trash are documented in [mapherez/nox-sync](https://github.com/mapherez/nox-sync). This repository maintains the backend and the API used by that plugin.

## Dashboard Says Google OAuth Is Not Configured

The backend is running, but one or both Google OAuth variables are missing.

Check your `.env` file:

```bash
NOX_BACKEND_GOOGLE_CLIENT_ID=your-google-client-id
NOX_BACKEND_GOOGLE_CLIENT_SECRET=your-google-client-secret
NOX_BACKEND_ADMIN_EMAILS=you@example.com
```

Then restart the container:

```bash
docker compose up -d
```

## Google Login Fails Or Redirects Back With An Error

The most common cause is a mismatch between:

- `NOX_BACKEND_PUBLIC_URL`
- The Google OAuth redirect URI
- The URL you use in the browser

For a domain deployment, these should line up:

```bash
NOX_BACKEND_PUBLIC_URL=https://sync.example.com
```

```text
https://sync.example.com/auth/google/callback
```

```text
https://sync.example.com/vault-dashboard
```

For local testing with the default Compose port:

```bash
NOX_BACKEND_PUBLIC_URL=http://localhost:5710
```

```text
http://localhost:5710/auth/google/callback
```

```text
http://localhost:5710/vault-dashboard
```

## Google Login Works But Access Is Denied

The Google account is not allowlisted or has been disabled.

Make sure the first admin email is in `NOX_BACKEND_ADMIN_EMAILS`:

```bash
NOX_BACKEND_ADMIN_EMAILS=you@example.com
```

Emails are comma-separated:

```bash
NOX_BACKEND_ADMIN_EMAILS=you@example.com,other-admin@example.com
```

Bootstrap admin emails are created or restored as active admins on backend startup.

## `AUTH_FAILED`

The plugin reached the backend, but the API key was missing or invalid.

Check that the API key in plugin settings exactly matches the current key shown on the backend dashboard. If you generated a new key, update every existing device manually.

In the multi-user backend, API keys are per user. Make sure the key belongs to the same Google user that owns the backend vault you are selecting.

## Select Or Create Backend Vault

The plugin reached the backend, but no remote vault is selected.

Open NoX Sync settings, use the Backend vault section, then select a backend vault or create one from the current Obsidian vault name.

If a previously selected vault was deleted, restore it from the plugin settings or dashboard, or select a different vault before syncing again.

If a vault was permanently deleted, it cannot be restored. Create a new backend vault and sync again.

When no backend vault is selected, clicking the NoX Sync ribbon icon opens the NoX Sync settings tab directly.

## `SERVER_UNREACHABLE`

The plugin could not reach the backend.

Check that the container is running:

```bash
docker compose ps
```

Check backend logs:

```bash
docker compose logs nox-backend
```

Verify the Server URL in plugin settings, including protocol and port.

For the default Compose file, local Server URL is:

```text
http://localhost:5710
```

If you use a domain or reverse proxy, the plugin Server URL should be the same public origin as `NOX_BACKEND_PUBLIC_URL`.

## Docker Container Is Running But Browser Cannot Open The Dashboard

Check the port mapping:

```bash
docker compose ps
```

The provided Compose file maps:

```text
5710:8080
```

Open:

```text
http://localhost:5710/vault-dashboard
```

If another service already uses port `5710`, change the left side of the port mapping in `docker-compose.yml`, for example:

```yaml
ports:
  - "5711:8080"
```

Then open:

```text
http://localhost:5711/vault-dashboard
```

Also update `NOX_BACKEND_PUBLIC_URL` and the Google OAuth redirect URI to use the same port.

## `BLOCKED_REMOTE`

Another sync session currently owns the selected backend vault's sync lock.

Wait for the other device to finish. If that device crashed or lost connectivity, the backend heartbeat timeout eventually marks that vault's lock stale and unblocks future syncs.

Different backend vaults can sync at the same time.

## `CONFLICT`

Both local and remote versions changed since the last common synced revision.

The backend reports conflicting changes in the sync plan. Resolve them through the NoX Sync plugin; see its [instructions](https://github.com/mapherez/nox-sync).

NoX Sync does not silently overwrite conflicting changes.

## `ERROR`

The last sync failed in a recoverable or unsafe state.

Common causes include interrupted uploads, stale sessions, hash mismatches, or missing remote content. The plugin maps known backend error codes to safe retry behavior where possible. If retry fails repeatedly, check backend logs and preserve the vault before making manual changes.

## Stale Locks

During sync, the plugin sends regular heartbeats. If Obsidian closes, the network drops, or a sync hangs, heartbeats stop. The backend marks the lock stale after expiry, removes abandoned staging content, and broadcasts the stale state to connected clients.

## Deleted Vaults Still Use Space

Deleted backend vaults are soft-deleted first so they can be restored. Soft-deleted vaults can still use backend storage.

To reclaim backend space:

1. Open the dashboard or plugin settings.
2. Open the deleted vaults restore window.
3. Permanently delete the vault.

Permanent delete removes the vault metadata and then removes finalized blobs that are no longer referenced by any remaining vault.

## Backups

Back up the complete backend `/data` directory, not just the SQLite database. The database and `/data/blobs` directory must stay together.

If you use the production Compose file, the named volume key for `/data` is:

```text
nox-backend-data
```

The actual Docker volume name normally includes the Compose project prefix. If vaults disappear after the image switch, stop and verify that the container mounts the original data volume. Do not create replacement vaults, rename the project, reset the database, rotate keys, or run `docker compose down -v` to fix it. See the [repository separation and rollback guide](backend-separation.md).
Configuration compatibility: non-empty `NOX_BACKEND_*` values override matching
`NOX_SYNC_*` values. Legacy configurations continue to work. The physical database
name shown above is the new-installation default; existing `nox-sync.db` files are
used in place until an explicit offline migration.
