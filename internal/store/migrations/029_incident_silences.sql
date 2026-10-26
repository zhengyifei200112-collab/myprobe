CREATE TABLE incident_silences (
    id TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES alert_incidents(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL DEFAULT 1 CHECK(revision >= 1),
    starts_at_ms INTEGER NOT NULL CHECK(starts_at_ms >= 0),
    ends_at_ms INTEGER NOT NULL CHECK(ends_at_ms > starts_at_ms),
    reason TEXT NOT NULL CHECK(length(CAST(reason AS BLOB)) <= 1000),
    created_by TEXT NOT NULL,
    creator_name TEXT NOT NULL,
    acknowledge_requested INTEGER NOT NULL CHECK(acknowledge_requested IN (0,1)),
    canceled_at_ms INTEGER CHECK(canceled_at_ms >= starts_at_ms AND canceled_at_ms < ends_at_ms),
    canceled_by TEXT NOT NULL DEFAULT '',
    canceler_name TEXT NOT NULL DEFAULT '',
    closed_at_ms INTEGER CHECK(closed_at_ms >= starts_at_ms AND closed_at_ms < ends_at_ms),
    closure_reason TEXT NOT NULL DEFAULT '',
    CHECK(canceled_at_ms IS NULL OR closed_at_ms IS NULL),
    UNIQUE(id,incident_id)
);
CREATE INDEX idx_silences_incident_interval ON incident_silences(incident_id,starts_at_ms,ends_at_ms);

-- Results preserve the original operation, independently of later interval changes.
CREATE TABLE incident_silence_requests (
    request_id TEXT PRIMARY KEY,
    request_digest TEXT NOT NULL CHECK(length(request_digest)=64),
    silence_id TEXT NOT NULL REFERENCES incident_silences(id) ON DELETE CASCADE,
    result_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE incident_silence_decisions (
    incident_id TEXT NOT NULL,
    silence_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('creation','observation','observation_gap','delivery')),
    first_at_ms INTEGER NOT NULL,
    last_at_ms INTEGER NOT NULL CHECK(last_at_ms >= first_at_ms),
    PRIMARY KEY(silence_id,kind),
    FOREIGN KEY(silence_id,incident_id) REFERENCES incident_silences(id,incident_id) ON DELETE CASCADE
);
CREATE TABLE delivery_silence_links (
    delivery_id TEXT NOT NULL REFERENCES notification_deliveries(id) ON DELETE CASCADE,
    silence_id TEXT NOT NULL REFERENCES incident_silences(id) ON DELETE CASCADE,
    recorded_at_ms INTEGER NOT NULL,
    PRIMARY KEY(delivery_id,silence_id)
);
