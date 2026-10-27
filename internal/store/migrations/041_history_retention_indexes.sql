-- Keep original RFC3339Nano timestamp/payload bytes. A virtual UTC key pads
-- fractional seconds to 9 digits so comparisons preserve true time ordering.
ALTER TABLE metric_samples ADD COLUMN captured_sort_ns TEXT GENERATED ALWAYS AS (
    substr(captured_at,1,19)||'.'||substr(
      CASE WHEN substr(captured_at,20,1)='.' THEN substr(captured_at,21,length(captured_at)-21) ELSE '' END
      ||'000000000',1,9)||'Z') VIRTUAL;
ALTER TABLE latency_samples ADD COLUMN captured_sort_ns TEXT GENERATED ALWAYS AS (
    substr(captured_at,1,19)||'.'||substr(
      CASE WHEN substr(captured_at,20,1)='.' THEN substr(captured_at,21,length(captured_at)-21) ELSE '' END
      ||'000000000',1,9)||'Z') VIRTUAL;

-- Preserve epoch-based matching, including equivalent ISO timestamp spellings.
-- A lookup must address the exact node/target/kind/level/time bucket.
CREATE INDEX IF NOT EXISTS idx_metric_rollups_epoch_lookup
    ON metric_rollups(node_id,bucket_seconds,unixepoch(bucket_at));
CREATE INDEX IF NOT EXISTS idx_latency_rollups_epoch_lookup
    ON latency_rollups(node_id,target_id,kind,bucket_seconds,unixepoch(bucket_at));
CREATE INDEX IF NOT EXISTS idx_traffic_rollups_epoch_lookup
    ON traffic_rollups(node_id,bucket_seconds,unixepoch(bucket_at));

-- Filter eligible source/prune ranges before grouping instead of scanning all
-- nodes' retained days/year. Existing node/time indexes still serve history reads.
CREATE INDEX IF NOT EXISTS idx_metric_samples_retention_time ON metric_samples(captured_sort_ns);
CREATE INDEX IF NOT EXISTS idx_latency_samples_retention_time ON latency_samples(captured_sort_ns);
CREATE INDEX IF NOT EXISTS idx_metric_rollups_retention_time ON metric_rollups(bucket_seconds,bucket_at);
CREATE INDEX IF NOT EXISTS idx_latency_rollups_retention_time ON latency_rollups(bucket_seconds,bucket_at);
CREATE INDEX IF NOT EXISTS idx_traffic_rollups_retention_time ON traffic_rollups(bucket_seconds,bucket_at);
CREATE INDEX IF NOT EXISTS idx_metric_samples_node_capture_ns ON metric_samples(node_id,captured_sort_ns);
CREATE INDEX IF NOT EXISTS idx_latency_samples_node_target_capture_ns ON latency_samples(node_id,target_id,captured_sort_ns);
