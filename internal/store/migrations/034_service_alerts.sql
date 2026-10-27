-- Expand rule kinds without dropping owned legacy history.
CREATE TABLE _034_alert_events AS SELECT * FROM alert_events;
CREATE TABLE _034_alert_states AS SELECT * FROM alert_states;
DROP TABLE alert_events;
DROP TABLE alert_states;
CREATE TABLE alert_rules_v34 (
 id TEXT PRIMARY KEY,
 node_id TEXT REFERENCES nodes(id) ON DELETE CASCADE,
 channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('offline','cpu','memory','disk','latency','bandwidth','cycle_traffic','expiry','service_http')),
 config_json TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 cooldown_seconds INTEGER NOT NULL DEFAULT 900,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO alert_rules_v34 SELECT * FROM alert_rules;
DROP TABLE alert_rules;
ALTER TABLE alert_rules_v34 RENAME TO alert_rules;
CREATE TABLE alert_events (
 id TEXT PRIMARY KEY,
 rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
 node_id TEXT REFERENCES nodes(id) ON DELETE SET NULL,
 state TEXT NOT NULL CHECK(state IN ('firing','resolved','failed')),
 fingerprint TEXT NOT NULL,
 message TEXT NOT NULL,
 created_at TEXT NOT NULL,
 delivered_at TEXT,
 delivery_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO alert_events SELECT * FROM _034_alert_events;
DROP TABLE _034_alert_events;
CREATE INDEX idx_alert_events_fingerprint ON alert_events(fingerprint,created_at);
CREATE INDEX idx_alert_events_created_at ON alert_events(created_at DESC);
CREATE TABLE alert_states (
 fingerprint TEXT PRIMARY KEY,
 rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
 node_id TEXT REFERENCES nodes(id) ON DELETE CASCADE,
 active INTEGER NOT NULL CHECK(active IN (0,1)),
 last_message TEXT NOT NULL DEFAULT '',
 last_attempt_at TEXT NOT NULL,
 last_delivered_at TEXT,
 last_error TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL,
 pending_since TEXT
);
INSERT INTO alert_states SELECT * FROM _034_alert_states;
DROP TABLE _034_alert_states;
CREATE INDEX idx_alert_states_rule_id ON alert_states(rule_id);

CREATE TABLE http_service_alerts (
 service_id TEXT PRIMARY KEY REFERENCES http_services(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE RESTRICT,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 duration_seconds INTEGER NOT NULL CHECK(duration_seconds BETWEEN 0 AND 2592000),
 recovery_seconds INTEGER NOT NULL CHECK(recovery_seconds BETWEEN 0 AND 2592000),
 repeat_seconds INTEGER NOT NULL CHECK(repeat_seconds BETWEEN 0 AND 2592000),
 cooldown_seconds INTEGER NOT NULL CHECK(cooldown_seconds BETWEEN 30 AND 2592000),
 updated_at TEXT NOT NULL
);
-- Snapshot identities survive service/rule deletion, like incident subjects.
CREATE TABLE http_service_rule_bindings (
 service_id TEXT NOT NULL,
 node_id TEXT NOT NULL,
 rule_id TEXT NOT NULL UNIQUE,
 PRIMARY KEY(service_id,node_id)
);
CREATE INDEX idx_http_observer_latest ON http_tasks(service_id,node_id,revision,substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29),id);
