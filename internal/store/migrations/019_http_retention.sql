CREATE TABLE http_history_state (
    id INTEGER PRIMARY KEY CHECK(id=1),
    retained_from_ns INTEGER NOT NULL
);
INSERT INTO http_history_state(id,retained_from_ns) VALUES(1,0);
CREATE INDEX idx_http_tasks_scheduled ON http_tasks(scheduled_at);
