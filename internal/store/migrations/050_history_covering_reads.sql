-- Dense retained-history reads need the four aggregate values for every bucket.
-- Keep them next to the existing node/target/kind/tier/time seek keys, avoiding
-- a second B-tree lookup into the table for each matching retained row. Keep
-- the original unique constraint and retention indexes unchanged.
CREATE INDEX idx_latency_rollups_history_cover ON latency_rollups(
    node_id,target_id,kind,bucket_seconds,bucket_at,
    sample_count,success_count,latency_count,latency_sum
);
