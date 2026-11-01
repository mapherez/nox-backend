# Backend delivery and remaining product work

Point 8 closes the approved eight-point Backend implementation sequence. The
[progress record](suite-migration.md) records local validation and its exact scope.
Publishing and applying the resulting images remain operator-controlled actions.
This repository work does not itself push commits, run remote workflows, publish
a release, replace installed containers or modify installed volumes.

## Deliver the Backend

1. Review/commit the changes and push them to GitHub. Require the CI jobs for this
   exact commit to pass. They include Go race/vet/format checks, React checks/tests,
   release/readiness tooling, packaged version/health contracts, native Chromium
   flows and the full native Suite administration flow through real HTTPS.
2. Run the usual release process after CI approval. Release validates its tagged
   source and both images, including the same packaged version matrix and HTTPS
   browser flow before publishing. Stable/prerelease tagging and separate
   `nox-backend`/`nox-backend-frontend` images retain the existing behavior.
3. Back up the existing complete `/data` consistently. Preserve the actual volume,
   project, public origin, Google callback and configuration. Apply the selected
   published images using the existing Compose/Yard project and both services.
   Point 8 adds no database migration or new environment variable; migration 7
   and the existing encrypted private store remain the current schema/state.
4. Confirm native login, account/manager controls, personal keys, owned vaults,
   existing connected clients and the dependent frontend entry points on the
   installed deployment. Backend's compatibility tests retain those routes and
   authentication contracts. Real Google account consent and another application's
   complete runtime remain installed-deployment checks, not simulated claims.
5. On the existing HTTP deployment, Suite state inspection is available but secret
   operations stay blocked. For actual Suite pairing/execution, configure trusted
   internal HTTPS and the already documented target/proxy policies, then use the
   [native administration guide](suite-administration.md). No public exposure is
   required. Keep Suite MCP opt-in until its own enrollment is ready.

The separate frontend publication workflow still builds/checks only the frontend,
including its standalone proxy guard, and publishes an immutable commit tag.
A frontend-only replacement does not rebuild Backend or touch its data. A newer
frontend with an older Backend can keep native functions usable while a missing
Suite administration API is shown as unavailable; use matching versions for the
new integration controls.

## Validation boundaries

Tests use actual disposable Backend images/storage, the actual frontend/proxy,
the released SDK verifier/client and an explicitly identified HTTPS protocol
participant. They exercise actual pairing/ACK/registration and credential/key
rotation without requiring Gateway, Portal or an adapted Yard to exist.
No test participant is a shipped product or a product-specific contract.

The current dependent clients' personal-key `/v1/auth/check`, vault, file listing,
conditional download and sync contracts remain covered by the existing Backend
regression suite and image checks. Source references in NoX Sync and NoX Wiki Codex
still use those preserved routes; this work does not adapt or execute those other
applications. Browser session/redirect/key-copy/native form entry points remain
covered through the actual frontend image. Manual owner checks of earlier installed
Backend steps are recorded separately from these disposable tests.

Local completion does not claim a new GitHub run has passed, that unpublished
images are installed, or that the whole NoX Suite already runs end to end.

## Next separate implementations

Implement the Gateway and installer/product adapters against SDK `0.7.0` and Suite
wire `1.0`, then the dependent CLI flows. Build the Portal as its own application;
the Backend's native frontend stays in this project. Existing and future products
must implement the same contracts. Product-specific work does not require another
numbered Backend implementation phase merely because those products do not yet
exist; any subsequently discovered defect is handled as a concrete fix.
