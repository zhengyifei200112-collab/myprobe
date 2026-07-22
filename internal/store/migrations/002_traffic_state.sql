CREATE TABLE IF NOT EXISTS traffic_state (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    last_captured_at TEXT NOT NULL,
    last_rx_total INTEGER NOT NULL,
    last_tx_total INTEGER NOT NULL,
    cycle_rx_bytes INTEGER NOT NULL DEFAULT 0,
    cycle_tx_bytes INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
