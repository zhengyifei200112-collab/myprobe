-- Forward-only preparation track for a NEW COPY of a closed schema-013 backup.
-- These are the expression lookups already used by application migration 041.
-- No table, stored row, application migration receipt or retention policy changes.
CREATE INDEX IF NOT EXISTS idx_metric_rollups_epoch_lookup
    ON metric_rollups(node_id,bucket_seconds,unixepoch(bucket_at));
CREATE INDEX IF NOT EXISTS idx_latency_rollups_epoch_lookup
    ON latency_rollups(node_id,target_id,kind,bucket_seconds,unixepoch(bucket_at));
CREATE INDEX IF NOT EXISTS idx_traffic_rollups_epoch_lookup
    ON traffic_rollups(node_id,bucket_seconds,unixepoch(bucket_at));
