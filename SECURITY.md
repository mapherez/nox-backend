# Security Policy

## Supported versions

Security fixes are provided for the latest backend release/image maintained in this repository. Backend and its clients have independent releases.

Report vulnerabilities specific to independently maintained clients or NoX Auth
through their respective repositories. Backend's integration boundaries remain
in scope here.

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
- API key, NoX Login integration or session handling issues.
- Suite credential, delegation, pairing or authorization-boundary failures.
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

NoX Backend is self-hosted and owns user access, vault ownership, sync locks, remote state, and commits. The dashboard uses NoX Login through the shared Auth service, which owns Google authentication and the allowed-account list. Backend enforces local roles/status and data ownership; API clients use per-user `noxsync_` keys and central admission checks. Users cannot access each other's vaults.

Suite service operations use the shared same-host Unix channel with scoped
credentials and signed delegations. Native TCP/HTTPS access does not grant that
channel. The frontend receives neither the data volume nor service credentials;
only the one-shot Auth setup service receives the restricted setup volume.

Keep complete Backend backups, personal keys, login state and Suite secret storage
private. Report Backend integration vulnerabilities here; consumer-specific or
Auth-provider issues belong to their respective repositories.
