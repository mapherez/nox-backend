-- Connection intent only. Service credentials and pairing are separate trust state.
CREATE TABLE foundation_connections (
    service TEXT PRIMARY KEY CHECK (service IN ('yard', 'gateway')),
    origin TEXT NOT NULL,
    revision TEXT NOT NULL UNIQUE,
    updated_at TEXT NOT NULL
);
