CREATE TABLE http_schedule_epochs (
    service_id TEXT NOT NULL REFERENCES http_services(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK(revision > 0),
    start_ns INTEGER NOT NULL,
    end_ns INTEGER,
    interval_seconds INTEGER NOT NULL CHECK(interval_seconds BETWEEN 30 AND 86400),
    PRIMARY KEY(service_id,node_id,revision),
    CHECK(end_ns IS NULL OR end_ns >= start_ns)
);
CREATE INDEX idx_http_epochs_range ON http_schedule_epochs(service_id,node_id,start_ns);
