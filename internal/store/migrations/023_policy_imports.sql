CREATE TABLE alert_policy_imports (
    request_id TEXT PRIMARY KEY,
    request_digest TEXT NOT NULL,
    policy_ids_json TEXT NOT NULL CHECK(json_valid(policy_ids_json)),
    created_at TEXT NOT NULL
);
