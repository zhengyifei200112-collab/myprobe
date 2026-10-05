-- RFC3339Nano omits zero fractions; pad to a fixed nanosecond key for ordering.
CREATE INDEX idx_http_tasks_result_order ON http_tasks(
    service_id,
    substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29),
    id
);
