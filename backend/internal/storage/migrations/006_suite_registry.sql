-- Registry, resumable event generation and ordered availability are additive.
CREATE TABLE suite_registry_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 revision TEXT NOT NULL,
 generation TEXT NOT NULL,
 event_floor INTEGER NOT NULL DEFAULT 0
);
INSERT INTO suite_registry_state(singleton,revision,generation) VALUES(1,'registry_'||lower(hex(randomblob(16))),lower(hex(randomblob(16))));
CREATE TABLE suite_products (
 product_id TEXT PRIMARY KEY,
 revision TEXT NOT NULL UNIQUE,
 association_id TEXT NOT NULL,
 record_json TEXT NOT NULL
);
CREATE TABLE suite_product_tombstones (
 revision TEXT PRIMARY KEY,
 product_id TEXT NOT NULL,
 record_json TEXT NOT NULL,
 expires_at INTEGER NOT NULL
);
CREATE TABLE suite_observation_epochs (
 product_id TEXT NOT NULL,
 association_id TEXT NOT NULL,
 producer_id TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('yard','gateway')),
 epoch TEXT NOT NULL,
 PRIMARY KEY(product_id,association_id,producer_id,source)
);
CREATE TABLE suite_observations (
 product_id TEXT NOT NULL,
 association_id TEXT NOT NULL,
 producer_id TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('yard','gateway')),
 envelope_json TEXT NOT NULL,
 PRIMARY KEY(product_id,association_id,producer_id,source)
);
CREATE INDEX suite_trust_events_time ON suite_trust_events(occurred_at);
