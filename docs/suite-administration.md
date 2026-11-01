# Native Suite administration

Sign in to `/vault-dashboard` with an active Backend administrator account and
open **Manage Suite integrations**. This is the Backend's permanent native
frontend. Gateway and Portal do not need to exist or be available to administer
local trust, pair a service or verify a product registration.

The page supports any conforming product ID. Gateway, installer and product are
technical purposes, not a fixed list of NoX Suite applications. The existing
**Service connections** cards save the approved foundation addresses separately;
saving/removing an address does not establish/revoke trust or uninstall anything.

## Configuration and current state

`GET /api/admin/suite` supplies an authenticated, no-store native administration
projection. It accepts the existing personal admin key or Google session and
returns JSON, including on errors. It rechecks the current active admin role in
storage and reads a consistent snapshot of:

- Issuer, signing-key lifecycle and current conditional revisions.
- Current/provisioned service associations and live/current credential generations.
- Active pairings and the most recent 50 pairing outcomes, with expiry/revisions.
- Installer deployment authorizations, accepted registrations, blocked admission
  and separately timestamped lifecycle/MCP observations.
- Deployment configuration readiness and independently enabled MCP paths.

It does not return service credentials, pairing secrets, proofs, private signing
keys, verifiers or encrypted-store handles. The complete response is bounded at
2 MiB. The existing scoped management APIs remain available for reconciliation
if the complete native view exceeds that bound. Historical retired services and
old revoked credential generations are not an unbounded UI history.

The diagnostic read works on an authenticated native HTTP installation. Actual
Suite operations retain the existing trusted HTTPS requirement. Configure the
trusted public origin and [TLS/proxy boundary](suite-trust.md#tls-deployment-choices).
Target enrollment/verification also needs approved CIDRs and ports; local signing
key management and revocation do not depend on an outgoing target policy.
The page reports missing configuration and disables the affected controls.
Internal HTTPS over LAN/Twingate is supported; public Internet exposure is not
required. Existing native account/vault operations continue to work over their
existing supported transport.

A Suite dependency failure leaves native account/vault controls usable. The Suite
pane displays the failure and offers refresh; it does not turn an unknown state
into a successful connection. Observations expire to `unknown` in the view; opening
more browser sessions does not start extra product probes or invent readiness.

## Pair a service

1. Enter the real product ID, HTTPS origin and technical purpose in **Start product
   pairing**. For an installer-managed product, enter the authorized installer
   product/deployment IDs. Authorize that deployment in the installer's service
   controls first. Replacement uses the current trust-head revision and requires
   an explicit confirmation.
2. **Create pairing** returns the contract's intent as sensitive setup information.
   Transfer it securely to the product together with the Backend origin. The
   intent itself creates neither an acknowledged association nor membership.
3. The product generates its SDK pairing proof binding the intent, its actual
   declaration fingerprint and TLS public key. Its setup can submit that proof to
   the existing completion API. The native page also accepts the exact proof JSON
   under the pending pairing; Backend performs the same proof/pinned-HTTPS checks.
4. Transfer the returned provisioning to the intended product and save it securely
   there. The product acknowledges using its issued service credential. For manual
   setup, the native ACK control submits that credential as Bearer authorization
   only after explicit confirmation that the product has saved provisioning.
   An admin cookie cannot substitute for possession of the service credential.
5. The page now shows an acknowledged service. Use **Verify and register product**
   separately with the same actual ID/origin and management binding. Backend
   verifies the declaration again and conditionally commits membership.

A product's setup must implement the contract; an unchanged Yard/Gateway cannot
be made compatible by merely entering its address. Backend itself advertises
`nox-backend` and follows the same authorized pairing/registration protocol when
its Suite endpoint is enabled; its execution adapter reads its authority locally.
The page does not manufacture proofs for arbitrary unadapted products, accept
caller claims as verification, or infer trust from an HTTPS link.

A failed proof, pin, identity, address-policy or declaration check remains a visible
failure. Cancel unacknowledged pairing with its current revision when necessary;
this revokes its unacknowledged provisioning. Expired/replaced operations cannot
acknowledge or replace current trust. Acknowledgment alone still does not establish
MCP readiness.

## Credentials, signing keys and registration

Expand a service to inspect generations, rotate/revoke credentials or manage an
installer's deployment authorizations. A replacement credential is provisional
until the product saves and acknowledges it; its predecessor has the agreed bounded
overlap. ACK closes replacement recovery and revokes the predecessor. Current
credential recovery/expiry is shown independently of membership. Revoking a service
or deployment grant does not silently remove accepted registry membership.

Use **Verify and update** to re-fetch a registered product's declaration under the
current trust/revision. A changed origin/TLS identity requires authorized trust
reconciliation first. A known incompatible declaration keeps membership but blocks
Suite authorization until a fresh successful verified repair. Use **Remove
registration** to commit removal/tombstones and retire that association; this does
not uninstall containers or delete product data.

Prepare a signing key before activation. Prepared public keys are published while
the current signer remains active. Activation retires the previous verifier on the
contract schedule. Explicit revocation stops admission of its grants immediately
at this local authority. Revoking the active signer leaves issuance unavailable
until a prepared signer is activated; the page does not silently create another
issuer or pretend successful recovery.

## Lost responses and sensitive setup information

Suite mutations use the existing conditional revisions and durable Backend
idempotency outcomes. Before dispatch, this tab records its operation key and
non-secret request metadata in `sessionStorage`, partitioned by authenticated
Backend user ID. It stores no pairing secret/proof, service credential, provisioning
response, personal key or private signing material there.

- An unconfirmed transport/5xx response leaves **Operation awaiting confirmation**.
  No mutation is retried automatically. Refresh/reconcile actual state, or choose
  **Retry same Suite request**, which retains the exact key, body and preconditions.
- Sensitive requests need the original proof/credential entered again after reload.
  Its fingerprint must match before retry. A new request cannot overwrite an
  unresolved operation. Another user cannot adopt this tab's user-scoped journal.
- The last confirmed sensitive setup response retains only its non-secret recovery
  metadata. **Recover last setup response** explicitly replays the same operation;
  secret-bearing request fields must be re-entered where required. Backend still
  decides whether recovery is open; ACK/expiry closes it.
- **Clear setup information** removes the response from the page's memory. The
  separate **Forget setup recovery** control removes its non-secret receipt. Closing
  the browser tab ends its session storage; product setup must persist its own
  credential before ACK. Losing all recovery context requires reconciliation or
  cancellation/re-enrollment, not an invented replacement credential.
- A confirmed write followed by a failed refresh is shown as saved with refresh
  unavailable. Further Suite changes wait for a successful refresh. Stale revision
  and closed-recovery diagnostics are displayed; they never force-overwrite state.

This page uses the existing `/api/nox/*` routes and their payloads; it adds no new
Suite wire contract, role matrix, product catalog, CLI or Portal contribution API.
