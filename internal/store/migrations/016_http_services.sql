CREATE TABLE http_services (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
    interval_seconds INTEGER NOT NULL CHECK(interval_seconds BETWEEN 30 AND 86400),
    spec_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE http_service_nodes (
    service_id TEXT NOT NULL REFERENCES http_services(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY(service_id, node_id)
);
CREATE INDEX idx_http_service_nodes_node ON http_service_nodes(node_id);
