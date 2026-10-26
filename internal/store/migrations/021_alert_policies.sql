CREATE TABLE alert_policies (
    id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL CHECK(revision > 0),
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE RESTRICT,
    definition_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Acquire the SQLite writer reservation before reading the complete policy set.
CREATE TABLE alert_policy_writer (id INTEGER PRIMARY KEY CHECK(id = 1), version INTEGER NOT NULL);
INSERT INTO alert_policy_writer(id, version) VALUES(1, 0);
