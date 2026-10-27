CREATE TABLE service_alert_imports (
 request_id TEXT PRIMARY KEY,
 request_digest TEXT NOT NULL CHECK(length(request_digest)=64),
 result_json TEXT NOT NULL,
 created_at TEXT NOT NULL
);
