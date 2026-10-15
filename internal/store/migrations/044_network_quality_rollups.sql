-- A rollup replaces its raw tasks in the same transaction. Empty plan slots
-- need no rows: immutable epochs remain the authority for planned counts.
CREATE TABLE network_quality_rollups (
    epoch_id TEXT NOT NULL REFERENCES network_schedule_epochs(id) ON DELETE CASCADE,
    bucket_seconds INTEGER NOT NULL CHECK(bucket_seconds IN (60,300)),
    bucket_at INTEGER NOT NULL CHECK(bucket_at>=0 AND bucket_at%bucket_seconds=0),
    summary BLOB NOT NULL CHECK(length(summary) BETWEEN 8 AND 66048),
    PRIMARY KEY(epoch_id,bucket_seconds,bucket_at)
);
CREATE INDEX idx_network_quality_rollups_retention
    ON network_quality_rollups(bucket_seconds,bucket_at,epoch_id);
CREATE INDEX idx_network_tasks_scheduled_retention
    ON network_probe_tasks(scheduled_at,epoch_id) WHERE source='scheduled';
CREATE INDEX idx_network_tasks_manual_retention
    ON network_probe_tasks(expires_at_ns,id) WHERE source='manual';
CREATE INDEX idx_network_epochs_retention
    ON network_schedule_epochs(ends_at,id) WHERE ends_at IS NOT NULL;
CREATE TABLE network_quality_history_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    raw_floor INTEGER NOT NULL DEFAULT 0,
    minute_floor INTEGER NOT NULL DEFAULT 0,
    retained_from INTEGER NOT NULL DEFAULT 0,
    CHECK(raw_floor>=minute_floor AND minute_floor>=retained_from AND retained_from>=0)
);
INSERT INTO network_quality_history_state(singleton) VALUES(1);
