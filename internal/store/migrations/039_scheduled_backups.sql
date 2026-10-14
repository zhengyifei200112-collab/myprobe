-- Operational backup jobs have no synthetic node or incident dependency.
CREATE TABLE backup_schedule_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    config_json TEXT NOT NULL CHECK(json_valid(config_json)),
    last_scheduled_date TEXT NOT NULL DEFAULT ''
);
INSERT INTO backup_schedule_state(singleton,config_json) VALUES(1,'{"enabled":false,"directory":"","daily_time":"03:00","timezone":"UTC","keep":7,"failure_channel_ids":[]}');

CREATE TABLE backup_runs (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    scheduled_date TEXT UNIQUE,
    trigger_kind TEXT NOT NULL CHECK(trigger_kind IN ('daily','manual')),
    status TEXT NOT NULL CHECK(status IN ('running','completed','failed','interrupted')),
    started_at TEXT NOT NULL,
    finished_at TEXT,
    error_code TEXT NOT NULL DEFAULT '',
    artifact_json TEXT NOT NULL DEFAULT 'null' CHECK(json_valid(artifact_json)),
    failure_channel_ids_json TEXT NOT NULL CHECK(json_valid(failure_channel_ids_json)),
    CHECK((status='running')=(finished_at IS NULL))
);

CREATE TABLE system_notifications (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    backup_run_id TEXT NOT NULL REFERENCES backup_runs(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL,
    channel_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    channel_revision INTEGER NOT NULL,
    error_code TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','inflight','delivered','failed','unknown','canceled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 5),
    available_at_ms INTEGER NOT NULL,
    lease_token TEXT,
    lease_until_ms INTEGER,
    send_authorized INTEGER NOT NULL DEFAULT 0 CHECK(send_authorized IN (0,1)),
    error_class TEXT NOT NULL DEFAULT '',
    finished_at TEXT,
    UNIQUE(backup_run_id,channel_id),
    CHECK((status='inflight')=(lease_token IS NOT NULL AND lease_until_ms IS NOT NULL)),
    CHECK((status IN ('delivered','failed','unknown','canceled'))=(finished_at IS NOT NULL))
);
CREATE INDEX idx_system_notifications_due ON system_notifications(status,available_at_ms,seq);
CREATE INDEX idx_system_notifications_lease ON system_notifications(status,lease_until_ms);
