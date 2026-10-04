CREATE TABLE http_tasks (
    id TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES http_services(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK(revision > 0),
    scheduled_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    task_json TEXT NOT NULL,
    result_json TEXT,
    received_at TEXT,
    UNIQUE(service_id, node_id, revision, scheduled_at),
    CHECK((result_json IS NULL) = (received_at IS NULL))
);
CREATE INDEX idx_http_tasks_service_time ON http_tasks(service_id, scheduled_at);
CREATE INDEX idx_http_tasks_expiry ON http_tasks(expires_at);
