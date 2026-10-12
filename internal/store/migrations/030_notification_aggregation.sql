-- Logical deliveries retain their identities; transport aggregates own leases
-- and immutable actual-send payloads, with exact per-attempt membership.
CREATE TABLE notification_aggregates (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    policy_id TEXT NOT NULL,
    policy_revision INTEGER NOT NULL CHECK(policy_revision >= 1),
    channel_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    notification_type TEXT NOT NULL CHECK(notification_type IN ('firing','resolved')),
    starts_at_ms INTEGER NOT NULL,
    ends_at_ms INTEGER NOT NULL CHECK(ends_at_ms > starts_at_ms),
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','inflight','finished')),
    sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0,1)),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
    available_at_ms INTEGER NOT NULL,
    lease_token TEXT,
    lease_until_ms INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT,
    CHECK((status='inflight' AND lease_token IS NOT NULL AND lease_until_ms IS NOT NULL) OR (status!='inflight' AND lease_token IS NULL AND lease_until_ms IS NULL)),
    CHECK((status='finished')=(finished_at IS NOT NULL))
);
CREATE INDEX idx_aggregates_due ON notification_aggregates(status,available_at_ms,seq);
CREATE INDEX idx_aggregates_collecting ON notification_aggregates(policy_id,policy_revision,channel_id,provider,notification_type,status,sealed,ends_at_ms);
ALTER TABLE notification_deliveries ADD COLUMN aggregate_id TEXT REFERENCES notification_aggregates(id);
CREATE INDEX idx_delivery_aggregate ON notification_deliveries(aggregate_id,status,available_at_ms);
CREATE TABLE notification_aggregate_attempts (
    aggregate_id TEXT NOT NULL REFERENCES notification_aggregates(id),
    attempt_number INTEGER NOT NULL CHECK(attempt_number >= 1),
    lease_token TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    outcome TEXT NOT NULL CHECK(outcome IN ('prepared','started','delivered','failed','unknown','canceled')),
    error_class TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload_json)),
    PRIMARY KEY(aggregate_id,attempt_number),
    UNIQUE(aggregate_id,lease_token),
    CHECK((outcome IN ('prepared','started'))=(completed_at IS NULL))
);
CREATE TABLE notification_aggregate_attempt_members (
    aggregate_id TEXT NOT NULL,
    aggregate_attempt INTEGER NOT NULL,
    delivery_id TEXT NOT NULL,
    delivery_attempt INTEGER NOT NULL,
    included INTEGER NOT NULL DEFAULT 0 CHECK(included IN (0,1)),
    PRIMARY KEY(aggregate_id,aggregate_attempt,delivery_id),
    FOREIGN KEY(aggregate_id,aggregate_attempt) REFERENCES notification_aggregate_attempts(aggregate_id,attempt_number),
    FOREIGN KEY(delivery_id,delivery_attempt) REFERENCES delivery_attempts(delivery_id,attempt_number) ON DELETE CASCADE
);
