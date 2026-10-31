-- Additive Suite authority/trust state. No existing client/account data is reset.
CREATE TABLE suite_authority (
 singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
 issuer TEXT NOT NULL,
 active_key TEXT NOT NULL,
 revision TEXT NOT NULL,
 last_clock_ms INTEGER NOT NULL
);
CREATE TABLE suite_secret_handles (
 handle TEXT PRIMARY KEY,
 expires_at INTEGER NOT NULL
);
CREATE TABLE suite_signing_keys (
 id TEXT PRIMARY KEY,
 state TEXT NOT NULL CHECK (state IN ('prepared','active','retiring','revoked')),
 public_json TEXT NOT NULL,
 secret_handle TEXT NOT NULL,
 retire_at INTEGER NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE TABLE suite_services (
 id TEXT PRIMARY KEY,
 product_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('provisioned','active','revoked')),
 record_json TEXT NOT NULL
);
CREATE TABLE suite_trust_heads (
 product_id TEXT PRIMARY KEY,
 service_id TEXT NOT NULL REFERENCES suite_services(id),
 revision TEXT NOT NULL
);
CREATE TABLE suite_credentials (
 id TEXT PRIMARY KEY,
 service_id TEXT NOT NULL REFERENCES suite_services(id),
 verifier TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL CHECK (state IN ('provisioned','active','overlap','revoked')),
 not_after INTEGER NOT NULL,
 record_json TEXT NOT NULL
);
CREATE INDEX suite_credentials_service ON suite_credentials(service_id);
CREATE TABLE suite_pairings (
 id TEXT PRIMARY KEY,
 product_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('pending','provisioned','acknowledged','cancelled','expired')),
 expires_at INTEGER NOT NULL,
 record_json TEXT NOT NULL
);
CREATE UNIQUE INDEX suite_pairings_pending ON suite_pairings(product_id) WHERE state IN ('pending','provisioned');
CREATE TABLE suite_deployments (
 service_id TEXT NOT NULL REFERENCES suite_services(id),
 deployment_id TEXT NOT NULL,
 product_id TEXT NOT NULL,
 origin TEXT NOT NULL,
 revision TEXT NOT NULL,
 PRIMARY KEY (service_id, deployment_id)
);
CREATE TABLE suite_idempotency (
 principal TEXT NOT NULL,
 operation TEXT NOT NULL,
 key_hash TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 status INTEGER NOT NULL,
 response_json TEXT NOT NULL,
 secret_handle TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 PRIMARY KEY (principal, operation, key_hash)
);
-- Committed invalidations are retained here for the event-delivery work in point 5.
CREATE TABLE suite_trust_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 event_id TEXT NOT NULL UNIQUE,
 type TEXT NOT NULL,
 event_json TEXT NOT NULL,
 occurred_at INTEGER NOT NULL
);

CREATE INDEX suite_credentials_expiry ON suite_credentials(state,not_after);
CREATE INDEX suite_secret_expiry ON suite_secret_handles(expires_at);
