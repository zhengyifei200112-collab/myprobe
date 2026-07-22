CREATE TABLE IF NOT EXISTS metric_rollups (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    bucket_seconds INTEGER NOT NULL CHECK(bucket_seconds IN (60, 300)),
    bucket_at TEXT NOT NULL,
    sample_count INTEGER NOT NULL,
    cpu_sum REAL NOT NULL,
    memory_percent_sum REAL NOT NULL,
    disk_percent_sum REAL NOT NULL,
    net_rx_rate_sum REAL NOT NULL,
    net_tx_rate_sum REAL NOT NULL,
    PRIMARY KEY(node_id, bucket_seconds, bucket_at)
);
CREATE INDEX IF NOT EXISTS idx_metric_rollups_node_time
    ON metric_rollups(node_id, bucket_seconds, bucket_at);

CREATE TABLE IF NOT EXISTS latency_rollups (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('ping', 'tcping')),
    bucket_seconds INTEGER NOT NULL CHECK(bucket_seconds IN (60, 300)),
    bucket_at TEXT NOT NULL,
    sample_count INTEGER NOT NULL,
    success_count INTEGER NOT NULL,
    latency_count INTEGER NOT NULL,
    latency_sum REAL NOT NULL,
    PRIMARY KEY(node_id, target_id, kind, bucket_seconds, bucket_at)
);
CREATE INDEX IF NOT EXISTS idx_latency_rollups_node_time
    ON latency_rollups(node_id, bucket_seconds, bucket_at);

CREATE TABLE IF NOT EXISTS traffic_rollups (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    bucket_seconds INTEGER NOT NULL CHECK(bucket_seconds IN (60, 300)),
    bucket_at TEXT NOT NULL,
    rx_bytes INTEGER NOT NULL,
    tx_bytes INTEGER NOT NULL,
    PRIMARY KEY(node_id, bucket_seconds, bucket_at)
);
CREATE INDEX IF NOT EXISTS idx_traffic_rollups_node_time
    ON traffic_rollups(node_id, bucket_seconds, bucket_at);
