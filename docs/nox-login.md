# NoX Login in Backend

Backend uses the shared, headless NoX Auth service. Google credentials and the
allowed-account list belong only to Auth. The Backend retains its native
frontend, user roles, personal API keys, vault ownership and client APIs.

## Installation and update order

Backend requires Auth with private `POST /v1/admission` support. Auth `v0.0.1`
lacks it. **Install/update compatible Auth first, then Backend and its frontend.** The Backend Go client uses the
published Auth `v0.0.1` package; the additional endpoint is a service capability,
not an unpublished Go dependency. Setup refuses an older Auth with an explicit
update message before the normal Backend container starts.

Run Auth on the same Docker host first. Its normal Compose creates the shared
runtime and restricted setup volumes. Keep those volume names consistent across
projects; the Backend declares them external. No certificate copying or manual
client IDs/secrets are needed. Set only these required Backend settings:

```env
NOX_BACKEND_PUBLIC_URL=http://backend.home.arpa:5710
NOX_AUTH_PUBLIC_URL=https://auth.example.com
```

Use the address actually opened in the browser, including a non-default port.
HTTP private hostnames/IPs and HTTPS application origins are supported. Auth's
Google callback remains its own HTTPS URL. Backend's `/auth/nox/callback` is
registered automatically with Auth; do not register it with Google.

For an existing installation, preserve its data-volume name and any optional
settings. Back up the complete data volume, retain the previous Compose, `.env`
and image tags, and then use the updated Compose:

```sh
docker compose pull
docker compose up -d
```

The one-shot `auth-setup` service enrolls an installation-specific client using
Auth's restricted administration socket. The generated identity/secret persist
in Backend data and are reused on subsequent setup. The normal Backend receives
only the runtime socket volume. Neither image contains a second Google login
implementation. Changing the issuer is refused rather than replacing trusted
identity silently. Changing the application origin requires re-running setup
with that exact origin and opening the new address.

## Identity and existing data

Verified Auth identity includes the original Google subject. Existing accounts
are matched by that subject, or by verified email for a previously unlinked local
record. Conflicting identity bindings are refused. Existing user IDs, roles,
status, keys and vault ownership are retained. A newly admitted account receives
an ADMIN account; an existing USER is not silently promoted. Admitted ADMIN
accounts signed in through NoX Login have Admin Manager authority; self-deletion,
self-disable and self-role changes remain forbidden.

Backend user administration remains local account/data administration. Creating
or enabling a record there does not add an email to Auth's allowlist. Disabled
Backend accounts remain denied even when Auth admits the Google account.

Auth state is in a separate private `nox-login.db` beside the existing main
database. Its client secret and access tokens stay server-side; the browser
receives an opaque session cookie. Back up the whole data volume, including this
file and the original database/blobs. The original main schema/history remains
unchanged by this adapter. Previous native-Google sessions require one new NoX
Login; existing API keys do not require rotation. `/auth/google/start` remains an
alias for integrations that open the former login entry; the callback now belongs
to NoX Login.

Reverting to the preceding Backend image requires its matching previous Compose,
frontend and Google configuration. The original database remains readable, but a
live Google rollback is operator acceptance; do not treat schema-preservation
unit tests as an installed rollback test. Never reset the database to recover login.

## Sessions, admission and logout

Sessions last one year and renew with admitted use. Backend validates browser
sessions locally with Auth at most every five minutes while active. It renews
the Auth browser session through the normal redirect when due, without another
Google selection if that session is still valid. Tokens persist across ordinary
Backend updates. No Google request occurs for routine admission checks.

Personal API keys retain their own authentication, ownership and revocation.
After verifying a key, Backend checks its account's central admission through
`/v1/admission`, with at most five minutes of positive reuse. Removing an account
from the applied Auth configuration blocks subsequent key access within that
bound; it does not rewrite the key. Validation failure never extends expired
admission. Long-lived direct sync SSE connections also revalidate. Suite
operation delegations cannot outlive the admission result used to issue them.

- **Log out** clears this application's browser session. It leaves the Auth
  browser session, other applications and independent personal API keys intact.
  Local logout still clears Backend state if Auth is unavailable.
- **Sign out of all NoX apps** in the Backend dashboard requests revocation of
  the current Auth root session and its derived application sessions. Other apps
  enforce this within five minutes. It does not sign out Google, other devices,
  Discord integrations or independently valid API keys. Failure is shown as an
  error; Backend does not claim global revocation after clearing only its cookie.

Both JSON logout routes require the browser session and same-origin mutation
checks; a Bearer key cannot log out a different browser. Service enrollment,
deployment authorization and existing Suite grants are unchanged.

## Verification and limits

Go tests cover signed OIDC/PKCE, callback binding/replay, existing identity/data
preservation, persistent sessions, account removal/outage and logout scopes.
Packaged split/browser and local-Suite checks use explicitly disposable identity
fixtures; fixture code is absent from the runtime binary. The additional
`check-auth-compose.py` executes the shipped Compose with a real Auth image,
checks setup, private HTTP callback, cross-project volumes and restart, and
removes its resources. It never contacts Google or installed data.

On November 4, 2026, after updating Auth, Backend and Yard, the owner confirmed
Google login in both applications. Backend's **Logout all** also logged out Yard
when switching to its tab, and both applications accepted a new login afterwards.
This records owner-reported installed acceptance of those login, shared-session
revocation and re-login flows. This adaptation does not complete Yard lifecycle
authorization, product MCP metadata, Gateway/CLI work or the full Suite integration.
