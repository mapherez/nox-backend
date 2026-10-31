# Backend Storage

Backend metadata uses SQLite at:

```text
/data/nox-backend.db
```

File contents are stored outside SQLite:

```text
/data/
  nox-backend.db
  blobs/
  staging/
  logs/
```

## Model

The multi-user, multi-vault schema stores:

- users and Google identity metadata
- server-side web sessions
- OAuth state tokens
- per-user API keys
- per-user vaults
- vault-scoped files, revisions, tombstones, locks, sessions, plans, uploads, and conflicts

Finalized file content is stored as content-addressed blobs under `/data/blobs/{sha256}`.

Upload content is staged under `/data/staging/{sessionId}` until a sync commit succeeds or the session is aborted/reaped.

## Migrations

Migrations live in `migrations/` and are applied in numeric order. The `schema_migrations` table records applied versions.

Startup validates any existing database before applying migrations. New databases are initialized automatically.

The historical migration 3 contains a destructive reset. Existing databases must
already have the intact multi-user/multi-vault baseline (1, 2, 3); that reset never
runs as an automatic upgrade. Later additive migrations are applied only after
validating the exact schema corresponding to the recorded history. Migration 4
adds non-secret foundation connection intent (origin, opaque revision, timestamp).
Unknown histories, missing historical baseline and schema mismatches are refused
before modification. Older binaries reject migration 4; retain a pre-upgrade
backup for rollback. See [administration persistence](../../../docs/administration-api.md#persistence-and-delivery).

Existing `nox-sync.db` is used in place. `nox-backend migrate-database` performs an explicit offline SQLite copy to `nox-backend.db`, retaining `nox-sync.db.legacy`. Both active filenames at once, or an interrupted migration with only the archive left, cause startup to fail rather than create an empty replacement. Back up all of `/data` before running the command. See [the update guide](../../../docs/backend-separation.md).

## Delete Behavior

Vault delete is soft by default:

- the vault status becomes `DELETED`
- normal sync/download/list operations hide or reject it
- the vault can be restored

Permanent delete removes the deleted vault row and cascades its vault-scoped metadata. After the transaction commits, storage cleanup removes finalized blob files that are no longer referenced by any remaining file or revision metadata.

Shared content-addressed blobs remain on disk if any remaining vault still references the same SHA-256 hash.


## Suite trust storage

Migration 5 adds persistent issuer/public-key metadata, service credential
verifiers and generations, associations, pairing/idempotency recovery handles,
installer deployment grants and transactional invalidation records. It does not
reset account/vault data. Private Suite material is stored as AES-256-GCM files
under `/data/secrets/suite` (0700 directory, 0600 files/master key), not plaintext
in SQLite or raw idempotency outcomes. The secret-store master key is never
silently regenerated for an existing authority. Back up SQLite/WAL and the entire
secret store together. See [Suite trust](../../../docs/suite-trust.md).
