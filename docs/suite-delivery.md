# Backend releases, updates and validation

This guide covers the current Backend project: separate Backend/frontend images,
NoX Login, persistent data and same-host Suite integration. The
[implementation record](suite-migration.md) is historical evidence, not deployment
instructions. For a new installation use [setup](user-setup.md).

## Release from source

Use `npm run release` from a clean repository checkout with the required Go,
Node/npm, Git and Docker tools. The script validates the version, Go/frontend
code and both image builds before committing and pushing the version/tag.
GitHub CI and Release perform their own checks and publication. A local success
or push is not confirmation that the remote workflow or publication succeeded.
See the [release overview](../README.md#releases-and-updates).

Stable releases publish versioned Backend/frontend images and update both
`latest` tags. Prereleases leave `latest` unchanged. A separate frontend workflow
can publish a compatible immutable `sha-<commit>` image without rebuilding Backend;
see [independent frontend updates](frontend-deployment.md#published-images-and-independent-frontend-updates).

## Update an existing installation

1. Record the existing Compose project, origin/port, image references and actual
   data/runtime/setup volume names. Retain the previous Compose and `.env` privately.
2. Back up the complete Backend data consistently, including the main SQLite
   database and sidecars, blobs, `nox-login.db` and `secrets/suite`. Do not run
   `docker compose down -v` or replace an existing volume with a new default.
3. Ensure compatible NoX Auth is installed first. It must provide `/v1/admission`.
   Google credentials and allowed emails belong there, not in Backend's `.env`.
4. Apply relevant shipped Compose changes as well as images when upgrading an
   older topology: `auth-setup`, separate Backend/frontend services and the
   external runtime/setup volumes. Backend runtime mounts the shared local volume;
   only setup receives Auth's restricted admin volume. Preserve the actual data mount.
5. Select compatible image tags, then use the existing project/options:

```sh
docker compose pull
docker compose up -d
```

The frontend inherits `NOX_BACKEND_IMAGE_TAG` unless overridden by
`NOX_FRONTEND_IMAGE_TAG`. Pulling/recreating images alone does not add missing
services, mounts or configuration. Current default volume names and required
origins are documented in [configuration](backend-configuration.md).

Moving from native Google login to NoX Login requires one new browser login.
Existing identity bindings, roles, personal keys and vault ownership are retained;
no API-key rotation is required for the change. Auth setup persists and reuses its
installation-specific client. Do not copy or reveal generated setup credentials.

## Confirm the installed update

- Check successful Auth setup, Backend health and frontend dashboard access.
- Sign in through NoX Login; confirm account/manager controls, owned vaults and
  current API-key access. Check the existing clients and native frontend consumers.
- Verify file reads, conditional downloads and synchronization as applicable to
  the installation. These contracts do not require Suite membership.
- Check existing Yard/Gateway connections and recovery state. Their web URLs may
  use private HTTP; local service communication requires no certificates/CIDRs.
- If exposing Backend MCP through Gateway, enable
  `NOX_BACKEND_SUITE_MCP_ENABLED=true` and open the administrator dashboard with
  compatible Backend/frontend images. It publishes Backend automatically. Refresh
  the client's catalog and execute a read-only tool; do not equate a declaration
  or stored URL with registration or MCP readiness.

## Rollback and restore

A compatible frontend-only rollback changes no Backend data. A Backend rollback
across schema additions requires the previous binary/images and a matching full
pre-upgrade data backup. Do not run older binaries against a newer schema or edit
migration records to bypass validation. An authentication rollback also needs
its matching previous Compose and login configuration.

After restoring complete Suite authority from backup, follow the explicit offline
[event-cursor reset procedure](suite-registry.md#events-and-reconciliation).
Routine restarts do not require that reset. Restore matching secret storage with
SQLite; missing keys must not be replaced to force startup.

## Verification scope

CI/release checks include Go and frontend tests, disposable image/version checks,
NoX Auth Compose enrollment and native HTTP/local-Suite browser flows. The
[frontend guide](frontend-deployment.md#verification) lists the concrete scripts.
They use isolated accounts, sessions, storage and protocol participants, not
installed data or personal credentials.

These checks validate the interfaces they exercise. They do not establish that
any arbitrary product adapter works, that a remote workflow passed, or that the
complete installed Suite has been validated. Yard deployment behavior, Gateway
routing, CLI navigation and other products retain their own repositories and
validation. Backend remains the data/integration authority and keeps its native
frontend; no additional deployment manager or replacement Portal is introduced.
