# Repository separation, validation, update, and rollback

NoX Backend now owns the Go backend and Docker image. The Obsidian plugin remains in [mapherez/nox-sync](https://github.com/mapherez/nox-sync). The repository cleanup does not modify backend source, dependencies, dashboard, migrations, or the HTTP contract.

The new production image reference is `ghcr.io/mapherez/nox-backend:latest`. This guide is for a later deployment update, after the image has been built, tested, and published. Cleaning this repository does not update a running server.

## Compatibility guarantees

The separation retains:

- `/v1` routes, request and response formats, errors, and SSE status events.
- Dashboard and Google OAuth routes, session cookies, API keys, and vault IDs.
- All `NOX_SYNC_*` variables and the `nox-sync` executable.
- `/data/nox-sync.db`, blobs, staging, logs, and their existing layout.
- Existing migration files and startup behavior.
- Compose service/container names, ports, mounts, and the `nox-sync-data` volume key.

No schema change or new migration is part of this transition. Existing plugin installations keep their Server URL, API key, selected vault, and local sync state.

## Record the current deployment

Work in the **existing deployment folder**, with its original Compose project name and `.env`. If you normally pass `-p`, `-f`, or other Compose options, keep using exactly those options throughout this procedure.

Before changing anything:

1. Record the running image ID/digest and keep that image available for rollback. Record any mounts and existing image overrides.
2. Record the actual source volume or bind-mount path for `/data`. Check the current container's mounts with `docker inspect nox-sync`, or the actual container name if your deployment differs.
3. Record the resolved configuration with `docker compose config`. It may contain secrets; store it privately.
4. Confirm the public URL, ports, OAuth configuration, admin allowlist, and existing API key configuration will stay unchanged.

Compose normally prefixes volume names with its project name, which defaults to the deployment directory's name. Moving to a folder named `nox-backend` can therefore select a different volume even when the YAML still says `nox-sync-data`. See [Compose project names](https://docs.docker.com/compose/how-tos/project-name/) and [volume naming](https://docs.docker.com/reference/compose-file/volumes/).

Do not rename the deployment folder or project, change volume declarations, copy data to a new production location, or use `docker compose down -v`. An empty vault list after updating is a reason to stop and check the mount, not to create replacement vaults.

## Make a consistent validation copy

Let active syncs finish and pause client syncs. Stop the existing backend service briefly, without removing its container or volume:

```bash
docker compose stop nox-sync
```

Use your existing backup method to copy the **complete** mounted `/data` directory to an isolated validation location. Include any SQLite WAL/SHM files if present. Copying only the database or only blobs is insufficient. Restart the original service after the consistent copy is complete:

```bash
docker compose start nox-sync
```

Treat the copy and backup as secrets: they contain credentials and vault contents. Keep an untouched backup separate from the writable validation copy.

## Verify that no migrations are pending

Inspect the validation copy before starting the candidate backend. With SQLite tooling on the host, open the copied database read-only:

```bash
sqlite3 -readonly /path/to/validation-data/nox-sync.db "SELECT version, name, applied_at FROM schema_migrations ORDER BY version;"
```

This checkout contains these existing migrations:

| Version | Name |
| --- | --- |
| 1 | `initial` |
| 2 | `sync_plan_actions` |
| 3 | `multi_user_multi_vault_reset` |

All three must already be recorded as applied. If any are missing, stop the transition: the unchanged startup mechanism would apply pending migrations, and migration 3 contains a reset of older tables. If the database contains newer migrations than this checkout, stop as well; do not downgrade an unknown schema. Do not run SQL to mark migrations applied or alter their records.

Save the database schema and migration query results before validation. The candidate must not change either of them.

## Validate the candidate on the copy

Build the candidate locally:

```bash
docker build -t nox-backend:validation ./backend
```

Run it against the writable validation copy only, on a separate port, with the same runtime settings needed by the existing deployment. For example:

```bash
docker run --rm --name nox-backend-validation --env-file /path/to/private-runtime.env -e NOX_SYNC_ADDR=:8080 -e NOX_SYNC_DATA_DIR=/data -p 127.0.0.1:5711:8080 --mount type=bind,source=/absolute/path/to/validation-data,target=/data nox-backend:validation
```

The runtime env file must contain resolved values, not Compose interpolation expressions. Preserve the existing public URL and OAuth configuration; do not route real users to the validation instance. The validation port is accessed directly by test clients. Test dashboard display using an existing session on the copy or a separately isolated OAuth test configuration.

First perform read-only checks:

- `GET /v1/health` reports ready and uses `/data/nox-sync.db`.
- `GET /v1/auth/check` accepts the existing user's API key.
- `GET /v1/vaults` returns the expected vault IDs and metadata.
- File listing and downloads return existing content with the expected hashes.
- Schema and `schema_migrations` records match the saved versions.
- Existing vault revisions, keys, and finalized blob contents remain unchanged.

The existing startup behavior still bootstraps configured admins and recovers stale sync locks. Those runtime effects are not schema migrations; abandoned staging may be cleaned as before.

For an end-to-end plugin check, use the plugin from the other repository in a separate disposable Obsidian vault. Configure it with the validation URL and the same API key from the copied backend. Verify **Test connection**, vault listing, and upload/download in a disposable remote vault on the validation copy. Do not change settings or sync state in a real user vault, and do not perform this test against production.

Stop the validation container when finished. Keep its data separate from the production volume. Document the results before publishing or deploying.

## Switch the production image

After validation succeeds and the new image is published, edit only the image reference in the **existing** Compose deployment:

```yaml
services:
  nox-sync:
    image: ghcr.io/mapherez/nox-backend:latest
```

Leave every other deployment setting unchanged. Confirm that `docker compose config` resolves to the same project, ports, environment, and actual `/data` mount as before.

Pause client syncs and take a fresh consistent backup before the update. Then, using the original Compose options:

```bash
docker compose pull nox-sync
docker compose up -d --no-deps nox-sync
docker compose ps
docker compose logs --tail=100 nox-sync
```

Verify health, existing API key authentication, expected vaults, and an existing file download before resuming client syncs. The plugin Server URL, API key, and selected vault do not change.

## Roll back

If validation after the switch fails, pause clients. Restore the previous image reference in the existing Compose configuration, preferably the exact recorded digest or retained local image. Recreate only the backend service using the image already kept locally:

```bash
docker compose up -d --no-deps --pull never nox-sync
```

Reuse the same project, volume, mounts, paths, and credentials. Since the separation makes no schema change, rollback does not require a data migration.

Do not replace live data with the validation copy. If a separate data incident requires restoring a backup, stop the service and restore the complete consistent backup using the existing recovery procedure; restoring an older backup can discard later syncs.