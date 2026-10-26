-- Serve raw history payloads from the same bounded node/time seek. Keep the
-- original row identity immediately after time, ahead of payload columns, so
-- equal-time rows retain the old index's implicit rowid order for floating sums.
-- Existing uniqueness, retention and latest-row indexes remain unchanged.
CREATE INDEX idx_metric_samples_history_cover ON metric_samples(
    node_id,captured_sort_ns,id,captured_at,cpu_usage,
    memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,
    net_rx_total,net_tx_total
);
CREATE INDEX idx_latency_samples_history_cover ON latency_samples(
    node_id,target_id,captured_sort_ns,id,captured_at,kind,success,latency_ms
);
