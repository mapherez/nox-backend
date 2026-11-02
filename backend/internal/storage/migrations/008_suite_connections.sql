CREATE TABLE suite_connection_attempts (
 service TEXT PRIMARY KEY CHECK(service IN ('yard','gateway')),
 attempt_id TEXT NOT NULL UNIQUE,
 record_json TEXT NOT NULL
);
