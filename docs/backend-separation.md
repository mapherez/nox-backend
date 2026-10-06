# Backend refactor: compatibility, validation, update and rollback

NoX Backend is independent from the [NoX Sync plugin](https://github.com/mapherez/nox-sync).
The refactor changes internal names and the dashboard presentation, preserving
HTTP routes, JSON, error contracts, API keys, sessions, vault identities and sync behavior.
The nox-wiki-codex read integration keeps its conditional downloads and original headers.

## Configuration and storage compatibility

Use `NOX_BACKEND_*` for new configuration. All matching `NOX_SYNC_*` runtime
variables remain accepted: non-empty canonical value, then non-empty legacy
value, then the existing default. Compose resolves both names using the same rule.
The legacy executable `nox-sync` remains available. Container UID/GID stay 10001.

New databases use `/data/nox-backend.db`. An existing `/data/nox-sync.db` is used
in place, without automatic renaming. If both active files exist, startup fails
rather than choosing one. Database schema, migration records, blobs and staging
layout are unchanged. An interrupted filename migration cannot silently create
a replacement empty database.

Existing data volumes do not need renaming. In particular this remains valid:

```env
NOX_SYNC_DATA_VOLUME_NAME=nox-sync_nox-sync-data
```

It can later be replaced by the equivalent canonical variable, preserving its value:

```env
NOX_BACKEND_DATA_VOLUME_NAME=nox-sync_nox-sync-data
```

Do not replace a working deployment with the new-installation Compose example.
Keep its service/container names, project, public URL, ports and mounts. Proxies
or automation may depend on the container name; wiki import identity depends on
the public URL. Do not run `docker compose down -v`.

## Record and back up the current installation

1. Work in the existing deployment folder with its original Compose options and `.env`.
2. Record the running image ID/digest and retain the old image for rollback.
3. Check the actual `/data` mount with `docker inspect <actual-container-name>`.
4. Inspect the resolved Compose configuration privately; it can contain secrets.
5. Pause sync clients, let active operations finish, and stop the backend.
6. Back up the complete mounted `/data`, including SQLite WAL/SHM files if present.
   Keep this backup untouched and create a separate writable validation copy.

For updates using a named volume, make it explicit and external in the deployment
Compose file so a missing name fails instead of creating an empty volume:

```yaml
volumes:
  nox-backend-data:
    external: true
    name: "${NOX_BACKEND_DATA_VOLUME_NAME:-${NOX_SYNC_DATA_VOLUME_NAME:?Set the existing volume name}}"
```

Keep the existing logical volume key if your service references a different one.
For bind mounts, preserve the existing source path instead.

## Validate the data before starting the candidate

Inspect the copied database with read-only SQLite tooling:

```bash
sqlite3 -readonly /path/to/validation-data/nox-sync.db "SELECT version, name, applied_at FROM schema_migrations ORDER BY version; PRAGMA integrity_check;"
```

The supported migration history is exactly:

| Version | Name |
| --- | --- |
| 1 | initial |
| 2 | sync_plan_actions |
| 3 | multi_user_multi_vault_reset |

Existing databases with pending, newer or inconsistent migrations are refused.
Migration 3 contains a historical reset; do not execute it manually or mark it as
applied to bypass validation. Unsupported older databases need a separately
planned data migration. Fresh installations initialize an empty schema normally.

Build the candidate and run it against the validation copy on a separate port,
using the current settings. Never route normal clients to this instance.

```bash
docker build -t nox-backend:validation ./backend
docker run --rm --name nox-backend-validation --env-file /path/to/private-runtime.env -e NOX_BACKEND_ADDR=:8080 -e NOX_BACKEND_DATA_DIR=/data -p 127.0.0.1:5711:8080 --mount type=bind,source=/absolute/path/to/validation-data,target=/data nox-backend:validation
```

Check health, existing-key authentication, vault IDs, file metadata, bytes/hashes,
conditional download rejection on changed selections, dashboard login and roles.
Test plugin sync in a disposable vault on the copy; test wiki import using an
isolated wiki state/content directory. Compare schema, migration records, keys,
vault revisions and finalized blob hashes with the baseline.
Startup still bootstraps configured admins and recovers expired sync locks.
Those normal effects can update timestamps and abandoned session/staging state.

## Optional offline database filename migration

This is a separate, explicit step. First validate it on the copy with all backend
processes stopped. The command checks schema and SQLite integrity, obtains an
exclusive connection, checkpoints WAL, copies committed data with `VACUUM INTO`,
checks the copy and retains the source as `nox-sync.db.legacy`. It refuses to
overwrite an existing destination or archive. It needs free space for a database
copy in addition to your independent backup. It does not copy or alter blobs.

Using the already built candidate image:

```bash
docker run --rm -e NOX_BACKEND_DATA_DIR=/data --mount type=bind,source=/absolute/path/to/validation-data,target=/data nox-backend:validation migrate-database
```

After success, health reports `/data/nox-backend.db`. Existing keys, sessions,
vault IDs and revisions remain valid. An installation can also keep using the
legacy filename indefinitely; the volume name is independent of the DB filename.

For production, after a fresh backup and with the service stopped, run the same
command through the existing Compose service using the verified candidate image:

```bash
docker compose run --rm --no-deps nox-backend migrate-database
```

Use the actual existing service name (for example `nox-sync`) if it differs.
Do not run this via `docker exec` inside an active backend server.

## Update and rollback

Publish only after validation. Pin the candidate image to a version/digest. In
the existing deployment change the image reference; keep the real `/data` source,
public URL, credentials and service name. Confirm mounts before starting it.
Verify existing auth, vaults, downloads and wiki access before resuming clients.
An unexpectedly empty vault list means stop and inspect the mount, not recreate data.

Without the optional filename migration, rollback just reuses the previous image
and the same volume. If the filename was migrated, the old image needs the legacy
filename again. Stop the backend, take a fresh full backup and checkpoint the
current `nox-backend.db` before renaming it back to `nox-sync.db`. Confirm no old
active database would be overwritten. This retains writes made after the update.
Do not substitute the `.legacy` archive after clients have resumed: it is a
snapshot from migration time and would discard subsequent writes.

A consistent backup restore is a separate recovery operation and can discard
writes after that backup. Never restore the writable validation copy to production.
