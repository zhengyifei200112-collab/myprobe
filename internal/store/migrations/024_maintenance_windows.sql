CREATE TABLE maintenance_windows (
    id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL CHECK(revision > 0),
    reason TEXT NOT NULL,
    timezone TEXT NOT NULL,
    starts_at_ms INTEGER NOT NULL,
    ends_at_ms INTEGER NOT NULL CHECK(ends_at_ms > starts_at_ms),
    canceled_at_ms INTEGER,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- Historical membership survives node deletion. Windows are canceled, not deleted.
CREATE TABLE maintenance_window_nodes (
    window_id TEXT NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    PRIMARY KEY(window_id, node_id)
);
CREATE INDEX idx_maintenance_nodes_lookup ON maintenance_window_nodes(node_id, window_id);
CREATE TABLE maintenance_writer (id INTEGER PRIMARY KEY CHECK(id = 1), version INTEGER NOT NULL);
INSERT INTO maintenance_writer(id, version) VALUES(1, 0);
