-- Hour summaries retain weighted counts and sums. Source mutations invalidate
-- their affected hour in the same transaction. Pending hours read source rows
-- until bounded retention refreshes them.
CREATE TABLE latency_history_hours (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    hour_unix INTEGER NOT NULL,
    sample_count INTEGER NOT NULL,
    success_count INTEGER NOT NULL,
    latency_count INTEGER NOT NULL,
    latency_sum REAL NOT NULL,
    PRIMARY KEY(node_id,target_id,kind,hour_unix)
) WITHOUT ROWID;

-- Tombstones can outlive a cascaded parent deletion until the next refresh.
CREATE TABLE latency_history_hours_dirty (
    node_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    hour_unix INTEGER NOT NULL,
    PRIMARY KEY(node_id,target_id,kind,hour_unix)
) WITHOUT ROWID;

-- Legacy noncanonical or unaligned starts keep their exact original range rules.
CREATE INDEX idx_latency_rollups_unaligned_history ON latency_rollups(
    node_id,target_id,kind,bucket_seconds,bucket_at,
    sample_count,success_count,latency_count,latency_sum
) WHERE (strftime('%Y-%m-%dT%H:%M:%SZ',bucket_at)=bucket_at
         AND unixepoch(bucket_at)%bucket_seconds=0) IS NOT TRUE;

INSERT INTO latency_history_hours
SELECT node_id,target_id,kind,unixepoch(bucket_at)/3600*3600,
       SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
FROM latency_rollups
WHERE strftime('%Y-%m-%dT%H:%M:%SZ',bucket_at)=bucket_at
      AND unixepoch(bucket_at)%bucket_seconds=0
      AND EXISTS(SELECT 1 FROM nodes WHERE id=latency_rollups.node_id)
      AND EXISTS(SELECT 1 FROM targets WHERE id=latency_rollups.target_id)
GROUP BY node_id,target_id,kind,unixepoch(bucket_at)/3600*3600;

-- BEFORE INSERT also covers INSERT OR REPLACE when recursive delete triggers
-- are disabled. Failed statements and transaction rollback undo invalidation.
CREATE TRIGGER latency_history_hours_insert BEFORE INSERT ON latency_rollups
WHEN strftime('%Y-%m-%dT%H:%M:%SZ',NEW.bucket_at)=NEW.bucket_at
     AND unixepoch(NEW.bucket_at)%NEW.bucket_seconds=0
BEGIN
    DELETE FROM latency_history_hours WHERE node_id=NEW.node_id AND target_id=NEW.target_id
        AND kind=NEW.kind AND hour_unix=unixepoch(NEW.bucket_at)/3600*3600;
    INSERT OR IGNORE INTO latency_history_hours_dirty VALUES(
        NEW.node_id,NEW.target_id,NEW.kind,unixepoch(NEW.bucket_at)/3600*3600);
END;

CREATE TRIGGER latency_history_hours_delete BEFORE DELETE ON latency_rollups
WHEN strftime('%Y-%m-%dT%H:%M:%SZ',OLD.bucket_at)=OLD.bucket_at
     AND unixepoch(OLD.bucket_at)%OLD.bucket_seconds=0
BEGIN
    DELETE FROM latency_history_hours WHERE node_id=OLD.node_id AND target_id=OLD.target_id
        AND kind=OLD.kind AND hour_unix=unixepoch(OLD.bucket_at)/3600*3600;
    INSERT OR IGNORE INTO latency_history_hours_dirty VALUES(
        OLD.node_id,OLD.target_id,OLD.kind,unixepoch(OLD.bucket_at)/3600*3600);
END;

CREATE TRIGGER latency_history_hours_update_old BEFORE UPDATE ON latency_rollups
WHEN strftime('%Y-%m-%dT%H:%M:%SZ',OLD.bucket_at)=OLD.bucket_at
     AND unixepoch(OLD.bucket_at)%OLD.bucket_seconds=0
BEGIN
    DELETE FROM latency_history_hours WHERE node_id=OLD.node_id AND target_id=OLD.target_id
        AND kind=OLD.kind AND hour_unix=unixepoch(OLD.bucket_at)/3600*3600;
    INSERT OR IGNORE INTO latency_history_hours_dirty VALUES(
        OLD.node_id,OLD.target_id,OLD.kind,unixepoch(OLD.bucket_at)/3600*3600);
END;

CREATE TRIGGER latency_history_hours_update_new BEFORE UPDATE ON latency_rollups
WHEN strftime('%Y-%m-%dT%H:%M:%SZ',NEW.bucket_at)=NEW.bucket_at
     AND unixepoch(NEW.bucket_at)%NEW.bucket_seconds=0
BEGIN
    DELETE FROM latency_history_hours WHERE node_id=NEW.node_id AND target_id=NEW.target_id
        AND kind=NEW.kind AND hour_unix=unixepoch(NEW.bucket_at)/3600*3600;
    INSERT OR IGNORE INTO latency_history_hours_dirty VALUES(
        NEW.node_id,NEW.target_id,NEW.kind,unixepoch(NEW.bucket_at)/3600*3600);
END;
