-- Channel-scoped provider waits share one persisted destination revision.
ALTER TABLE notification_channels ADD COLUMN delivery_revision INTEGER NOT NULL DEFAULT 1 CHECK(delivery_revision>=1);
ALTER TABLE notification_channels ADD COLUMN delivery_paused_until_ms INTEGER;
-- History identity must not be reused when a pre-send reservation is refunded.
ALTER TABLE notification_deliveries ADD COLUMN attempt_sequence INTEGER NOT NULL DEFAULT 0 CHECK(attempt_sequence>=0);
UPDATE notification_deliveries SET attempt_sequence=attempt_count;
-- Existing leases may already have sent. Only new reservations start unsent.
ALTER TABLE notification_deliveries ADD COLUMN send_authorized INTEGER NOT NULL DEFAULT 1 CHECK(send_authorized IN (0,1));

-- Expand history numbering, preserving IDs, the AUTOINCREMENT high-water mark,
-- and exact aggregate associations under the migration's existing transaction.
CREATE TABLE _033_attempt_sequence AS SELECT seq FROM sqlite_sequence WHERE name='delivery_attempts';
CREATE TABLE _033_attempt_members AS SELECT * FROM notification_aggregate_attempt_members;
DROP TABLE notification_aggregate_attempt_members;
CREATE TABLE delivery_attempts_v33 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id TEXT NOT NULL REFERENCES notification_deliveries(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK(attempt_number>=1),
    lease_token TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    outcome TEXT NOT NULL CHECK(outcome IN ('started','delivered','failed','unknown','canceled')),
    error_class TEXT NOT NULL DEFAULT '',
    UNIQUE(delivery_id,attempt_number),
    CHECK((outcome='started')=(completed_at IS NULL))
);
INSERT INTO delivery_attempts_v33 SELECT * FROM delivery_attempts;
DROP TABLE delivery_attempts;
ALTER TABLE delivery_attempts_v33 RENAME TO delivery_attempts;
INSERT INTO sqlite_sequence(name,seq) SELECT 'delivery_attempts',seq FROM _033_attempt_sequence
    WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='delivery_attempts');
UPDATE sqlite_sequence SET seq=MAX(seq,COALESCE((SELECT seq FROM _033_attempt_sequence),0)) WHERE name='delivery_attempts';
DROP TABLE _033_attempt_sequence;
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
INSERT INTO notification_aggregate_attempt_members SELECT * FROM _033_attempt_members;
DROP TABLE _033_attempt_members;
