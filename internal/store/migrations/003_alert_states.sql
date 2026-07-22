CREATE TABLE IF NOT EXISTS alert_states (
    fingerprint TEXT PRIMARY KEY,
    rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    node_id TEXT REFERENCES nodes(id) ON DELETE CASCADE,
    active INTEGER NOT NULL CHECK(active IN (0, 1)),
    last_message TEXT NOT NULL DEFAULT '',
    last_attempt_at TEXT NOT NULL,
    last_delivered_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alert_states_rule_id ON alert_states(rule_id);
