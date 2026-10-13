-- Expand rule kinds without dropping owned legacy history.
CREATE TABLE _035_alert_events AS SELECT * FROM alert_events;
CREATE TABLE _035_alert_states AS SELECT * FROM alert_states;
DROP TABLE alert_events;
DROP TABLE alert_states;
CREATE TABLE alert_rules_v35 (
 id TEXT PRIMARY KEY,
 node_id TEXT REFERENCES nodes(id) ON DELETE CASCADE,
 channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('offline','cpu','memory','disk','latency','bandwidth','cycle_traffic','expiry','service_http','service_certificate')),
 config_json TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 cooldown_seconds INTEGER NOT NULL DEFAULT 900,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO alert_rules_v35 SELECT * FROM alert_rules;
DROP TABLE alert_rules;
ALTER TABLE alert_rules_v35 RENAME TO alert_rules;
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
INSERT INTO alert_events SELECT * FROM _035_alert_events;
DROP TABLE _035_alert_events;
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
INSERT INTO alert_states SELECT * FROM _035_alert_states;
DROP TABLE _035_alert_states;
CREATE INDEX idx_alert_states_rule_id ON alert_states(rule_id);

ALTER TABLE http_service_alerts ADD COLUMN certificate_reminders_enabled INTEGER NOT NULL DEFAULT 0 CHECK(certificate_reminders_enabled IN (0,1));

-- Persistent service/certificate/threshold identities survive source deletion.
CREATE TABLE http_certificate_reminders (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 service_id TEXT NOT NULL,
 certificate_sha256 TEXT NOT NULL CHECK(length(certificate_sha256)=64 AND certificate_sha256 NOT GLOB '*[^0-9a-f]*'),
 days_before INTEGER NOT NULL CHECK(days_before IN (14,7,1)),
 not_before TEXT NOT NULL,
 not_after TEXT NOT NULL,
 rule_id TEXT NOT NULL UNIQUE,
 queued_once INTEGER NOT NULL DEFAULT 0 CHECK(queued_once IN (0,1)),
 authorized_once INTEGER NOT NULL DEFAULT 0 CHECK(authorized_once IN (0,1)),
 skipped INTEGER NOT NULL DEFAULT 0 CHECK(skipped IN (0,1)),
 incident_id TEXT,
 delivery_id TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(service_id,certificate_sha256,days_before)
);
CREATE INDEX idx_http_certificate_reminders_service ON http_certificate_reminders(service_id,seq DESC);