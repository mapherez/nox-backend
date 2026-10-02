# Security Policy

## Supported versions

Security fixes are provided for the latest backend release/image maintained in this repository. Backend and plugin releases are independent.

The Obsidian plugin is maintained in [mapherez/nox-sync](https://github.com/mapherez/nox-sync); report plugin-specific vulnerabilities through that repository's security policy.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability.

Use [GitHub private vulnerability reporting](https://github.com/mapherez/nox-backend/security/advisories/new) if it is enabled for this repository.

If private reporting is not available, contact the maintainer through the [GitHub profile](https://github.com/mapherez) and include only enough public detail to establish contact. Avoid posting exploit details, private server URLs, API keys, vault contents, logs with secrets, or personal data in public issues.

Helpful reports include:

- Affected backend version, image digest, or commit.
- Whether the issue affects the backend API, storage, Docker image, dashboard, or GitHub workflows.
- Reproduction steps and expected impact.
- Relevant logs with secrets removed.

## Scope

In scope:

- Unauthorized access to another user's vaults.
- API key, OAuth, or session handling issues.
- Sync data corruption caused by backend logic.
- Path traversal or filesystem access outside intended backend data paths.
- Unsafe handling of uploaded or downloaded file content.
- Vulnerable Docker or GitHub Actions configuration.

Out of scope:

- Vulnerabilities in a user's hosting provider, reverse proxy, DNS, or Google account.
- Compromised machines or Obsidian installations.
- Social engineering.
- Denial-of-service reports without a practical security impact.
- Issues requiring already-stolen API keys, Google accounts, or server access.

## Security model

NoX Backend is self-hosted and owns user access, vault ownership, sync locks, remote state, and commits. The dashboard uses Google OAuth; API clients use per-user `noxsync_` keys. Users cannot access each other's vaults.

The separate NoX Sync plugin connects to the Server URL configured by the user. Keep backend `/data` backups and API keys private. Repository separation preserves the existing authentication and storage behavior.