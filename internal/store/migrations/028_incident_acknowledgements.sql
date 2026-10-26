-- Acknowledgement is independent of condition state and delivery authority.
-- Keep the original actor name even if the account is renamed or removed.
CREATE TABLE incident_acknowledgements (
    incident_id TEXT PRIMARY KEY REFERENCES alert_incidents(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    actor_name TEXT NOT NULL,
    acknowledged_at TEXT NOT NULL,
    note TEXT NOT NULL CHECK(length(CAST(note AS BLOB)) <= 1000),
    request_id TEXT NOT NULL UNIQUE,
    request_digest TEXT NOT NULL CHECK(length(request_digest) = 64)
);
