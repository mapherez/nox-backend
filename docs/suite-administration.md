# Native Suite administration

The current setup is documented in [same-host integration and automatic setup](suite-local.md). This supersedes earlier certificate/proxy/manual-JSON instructions for local Suite integration. Sign in to the Backend's permanent native `/vault-dashboard` with an active administrator account.

## Foundation service connections

Save the Yard/Gateway native HTTP or HTTPS URL, select **Verify**, inspect the identified local service and select **Connect**. Both participant services must run on the same host and provide their local Suite adapters. Backend executes pairing and registration; users do not transfer certificates, intents, proofs or credentials.

Saving the address alone establishes no trust. Verification establishes no membership. A receipt without credential ACK is shown as waiting; registration is reported only after Backend has verified it. MCP availability is tracked separately. Use **Resume connection** to reconcile an interrupted attempt, **Cancel connection** before changing a pending address, and refresh the dashboard after background reconciliation.

## Configuration and current state

`GET /api/admin/suite` supplies the authenticated no-store administration projection using an existing personal admin key or Google session. Current active role is rechecked in storage. It reports issuer/signing keys, conditional revisions, current/provisioned services and credentials, recent pairings, authorized deployment references, registrations and observations. It returns no service credentials, pairing secrets, proofs, private keys, verifiers or encrypted-store handles.

The complete view is bounded at 2 MiB. Historical retired services and old revoked credential generations are not an unbounded history. Use scoped advanced management operations if the snapshot exceeds the bound. A Suite dependency failure leaves native account/vault controls usable.

`configuration.backendOrigin` is the logical local origin. `transport` is `unix`; `canManage`/`canEnroll` describe local-channel configuration, independent of browser HTTPS. No CIDR/port allowlist or TLS proxy configuration is required for the local channel. Existing web/client security and authentication policies remain in effect.

## Advanced administration and recovery

**Manage Suite integrations** retains inspection, conditional/idempotent service/key revocation, signing-key lifecycle and registry recovery. Browser operations go through `/api/admin/suite/operations/*`, behind the existing active-admin and CSRF checks. Direct network `/api/nox/*` and `/mcp/suite` calls are refused, even with TLS or forwarding headers. Service credentials are not accepted through the human adapter; participant-only completion/acknowledgement must occur on the local channel.

The panel inspects pairing state and retains cancellation, service/credential revocation, registration recovery and signing-key management. It has no manual pairing/proof/provisioning forms or credential-copy controls. Use the connection cards for foundations and the adapted Yard for installed products. The local protocol retains credential rotation for participant integrations; this cleanup does not implement a new automatic rotation receiver in unadapted products. Product registration is not inferred from a product name, URL, socket or UI choice.

The browser journals only non-secret operation metadata, per user/tab. It retains durable keys for exact retry and marks uncertain responses for reconciliation. Private proof/credential bodies and secret results are not accepted as browser operations. Old manual-operation records must be reconciled and cleared rather than replayed. SHA-256 fingerprinting and secure random operation IDs work in HTTP contexts without relying on HTTPS-only browser APIs. Pairing and credential acknowledgement remain participant responsibilities, including private persistence before acknowledging.

Revocation invalidates active use; it does not uninstall products or delete their business data. Removing a saved web URL likewise does not uninstall or implicitly revoke a registered service. Yard remains responsible for deployment/removal lifecycle.

## Verification boundary

Current tests exercise the native dashboard/API, real local protocol exchanges, response loss and restart using disposable data. The existing native frontend and all Backend consumers remain supported by their own API contracts. Test fixtures do not claim that Gateway, CLI, Portal or the Yard adapter have already been implemented. See [delivery guidance](suite-delivery.md).
