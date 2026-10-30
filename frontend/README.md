# NoX Backend native frontend

React + TypeScript, built and checked with project-local Vite+ `1.4.0`.
This frontend remains part of `nox-backend`; it is independent of the future
Suite Portal. Backend owns sessions, Google credentials, API keys, data and policy.

Use Node 24.15 or later in the supported Node 24 line, or Node 26 or later.
Install the pinned lockfile with `npm ci`. No global Vite+ installation is required.

```sh
npm ci
npm run dev
npm run check
npm test
npm run build
```

The development server forwards backend routes to `http://localhost:8080`.
Start the Go service from `backend/` with a disposable `NOX_BACKEND_DATA_DIR`.
`NOX_FRONTEND_BACKEND_URL` can select another development Backend origin.
The production image serves the build with Nginx and forwards the same routes
on one public origin. The image contains no Go service, database or Google secret.

Native forms and redirects remain compatible. `GET /api/dashboard` supplies the
session-filtered initial view. Changes use the [authenticated JSON administration
APIs](../docs/administration-api.md), with same-origin session protection, visible
errors and refreshed state. Initial Yard/Gateway addresses are saved as configuration
intent and explicitly await pairing; they do not imply a connected/trusted service.
No personal key is persisted by React in localStorage or used to replace the
existing browser session. Presentation revisions and sizes are decimal strings
to retain 64-bit accuracy. Rendered controls do not grant permissions.

For image delivery and preservation of existing installations, see
[frontend/deployment](../docs/frontend-deployment.md).
