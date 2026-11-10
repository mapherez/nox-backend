# File read and conditional download API

NoX Backend provides committed file metadata and binary downloads for application
clients and external integrations. Clients use the same Backend origin and
personal API key as the [Stable Client API](client-api.md). No importer, publishing
workflow or consumer-specific UI is implemented by this API.

## Authentication and vault selection

Send the account's current key in the request header:

```http
Authorization: Bearer <API_KEY>
```

The Server URL is the Backend base origin without `/v1`. The values below are
examples, not working credentials:

```sh
SERVER_URL='http://backend.home.arpa:5710'
API_KEY='<your-personal-api-key>'

curl --fail --show-error \
  -H "Authorization: Bearer $API_KEY" \
  "$SERVER_URL/v1/auth/check"

curl --fail --show-error \
  -H "Authorization: Bearer $API_KEY" \
  "$SERVER_URL/v1/vaults"
```

`GET /v1/auth/check` returns the current identity, including `ok`, `user` and
`role`. `GET /v1/vaults` returns active owned vaults in `vaults`, with `vaultId`,
`name`, `revision`, `status`, `updatedAt` and `sizeBytes`. It may also return
`deletedVaults`; select an active vault for reads.

Access is restricted to the key owner's vaults, including for ADMIN accounts.
Another application's admin role does not grant additional Backend access.
Missing/invalid credentials, a rotated key or a disabled account receive 401;
central Auth admission also applies. See [NoX Login](nox-login.md).

## List committed files

```http
GET /v1/files?vaultId=<ID>
```

```sh
curl --fail --show-error --get \
  -H "Authorization: Bearer $API_KEY" \
  --data-urlencode 'vaultId=vault_example' \
  "$SERVER_URL/v1/files"
```

A successful 200 response uses `Content-Type: application/json` and
`Cache-Control: no-store`:

```json
{
  "vaultId": "vault_example",
  "serverRevision": 12,
  "files": [
    {
      "path": "World/Humans.md",
      "hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "size": 2048,
      "revision": 10
    }
  ]
}
```

- `files` includes all current, non-deleted files: text, images, other binary
  content and zero-byte files.
- Paths are vault-relative, use `/` and preserve stored names and case. Spaces,
  Unicode and equal filenames in different directories are supported. URL-encode
  query parameters as shown above.
- The list is flat, path-ordered and unpaginated. A consumer can reconstruct its
  folder tree; empty directories are not represented.
- `hash` is the lowercase 64-character SHA-256 hash of the original bytes. `size`
  is the byte count; `revision` identifies the commit that last changed the file.
- File contents, history, tombstones, staged uploads and internal host paths are
  excluded. An empty vault returns `files: []`, even after all files are deleted.
  A new vault starts at `serverRevision: 0`.

### Vault revisions and file revisions

`serverRevision` is the vault-wide revision advanced by commits. A file's
`revision` is the vault revision at its last change, not an independent counter.
In the example, the vault is at revision 12 while `World/Humans.md` remains at 10.
Other file changes can advance the vault revision without changing this file's
revision or hash. Conditional downloads compare the file revision.

Authorization, the vault revision and file metadata are read in one SQLite
transaction. The list represents coherent committed state during concurrent
writes. Consumers must preserve the full integer precision of revisions/sizes.

## Download the selected file

```http
GET /v1/files/download?vaultId=<ID>&path=<PATH>&expectedHash=<HASH>&expectedRevision=<REVISION>
```

`vaultId` and `path` are required. The optional conditions are independent:

| Parameter | Validation | Condition |
| --- | --- | --- |
| `expectedHash` | Lowercase 64-character SHA-256 hexadecimal string | Must match the current file hash. |
| `expectedRevision` | Non-negative decimal integer, at most `9223372036854775807` | Must match the current file revision. |

Present conditions cannot be empty or repeated. Signed, whitespace-padded,
fractional or out-of-range revisions return 400. Sending both values from the
listing also detects a file that changed and later returned to the same bytes.

```sh
curl --fail --show-error --get \
  -H "Authorization: Bearer $API_KEY" \
  --data-urlencode 'vaultId=vault_example' \
  --data-urlencode 'path=World/Humans.md' \
  --data-urlencode 'expectedHash=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  --data-urlencode 'expectedRevision=10' \
  --dump-header download-headers.txt \
  --output Humans.md \
  "$SERVER_URL/v1/files/download"
```

A successful 200 response contains the original bytes with these headers:

```http
Content-Type: application/octet-stream
Content-Length: 2048
X-NoX-Sync-Path: World/Humans.md
X-NoX-Sync-Hash: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
X-NoX-Sync-Revision: 10
Cache-Control: no-store
```

Backend checks the conditions against the same metadata used to select the blob,
then serves that blob. It does not substitute a later version after selection.
Clients can verify the bytes against the selected hash, size and response headers.
The `X-NoX-Sync-*` header names are retained wire identifiers.

Without conditions, the endpoint returns the current file selected during the
request. Existing clients and vault ZIP exports keep their existing behavior.

### Changes between listing and download

A valid condition that no longer matches returns 409 with JSON/no-store:

```json
{
  "code": "FILE_CHANGED",
  "message": "File changed since selection. Refresh the file list and select again."
}
```

No file bytes are sent on this failure. Handle `FILE_CHANGED` by refreshing the
list and reviewing the selection. Do not drop conditions to silently accept a
different version. Branch on the code, not the human-readable message.

A deleted or renamed file returns 404 at its old path, even with conditions.
Refresh the list to find a new path. Changes to other files do not prevent a
download whose own hash and revision still match.

There is no persistent snapshot or reservation of historical versions. Several
downloads do not form an atomic vault snapshot. A commit after validation does
not change the blob already selected for that individual response.

## Errors and synchronization

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `BAD_REQUEST` | Missing parameters, invalid path or malformed conditions. |
| 401 | `AUTH_REQUIRED` / `AUTH_FAILED` | Missing or invalid credentials, rotated key or disabled account. |
| 404 | `NOT_FOUND` | Missing, deleted or foreign vault; missing, deleted or renamed file. |
| 409 | `FILE_CHANGED` | The supplied hash or revision no longer matches. |
| 405 | `BAD_REQUEST` | A method other than GET on the file routes. |

Listing and download responses, including errors, use `Cache-Control: no-store`.
These reads are allowed during synchronization and expose only committed data.
They do not acquire sync locks, start sessions, change revisions, reap stale
locks, emit sync events, export historical versions or write to Backend.
