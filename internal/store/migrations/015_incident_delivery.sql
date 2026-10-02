-- Fault state is independent from notification delivery. Historical identifiers
-- are snapshots rather than cascading foreign keys, so deletion cannot erase an
-- incident's explanation. Only owned child delivery/attempt data cascades.
CREATE TABLE alert_incidents (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    fingerprint TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    node_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    rule_snapshot_json TEXT NOT NULL CHECK(json_valid(rule_snapshot_json)),
    state TEXT NOT NULL CHECK(state IN ('pending','firing','resolved')),
    started_at TEXT NOT NULL,
    fired_at TEXT,
    resolved_at TEXT,
    observed_at TEXT,
    updated_at TEXT NOT NULL,
    trigger_since TEXT,
    recovery_since TEXT,
    observation_stale INTEGER NOT NULL DEFAULT 0 CHECK(observation_stale IN (0,1)),
    last_message TEXT NOT NULL DEFAULT '',
    resolution_reason TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT 'live' CHECK(origin IN ('live','legacy')),
    approximate_start INTEGER NOT NULL DEFAULT 0 CHECK(approximate_start IN (0,1)),
    last_enqueued_at TEXT,
    notification_sequence INTEGER NOT NULL DEFAULT 0 CHECK(notification_sequence>=0),
    CHECK(state!='firing' OR fired_at IS NOT NULL),
    CHECK((state='resolved')=(resolved_at IS NOT NULL))
);
CREATE UNIQUE INDEX idx_incidents_one_active ON alert_incidents(fingerprint) WHERE state IN ('pending','firing');
CREATE INDEX idx_incidents_state_seq ON alert_incidents(state,seq DESC);
CREATE INDEX idx_incidents_node_seq ON alert_incidents(node_id,seq DESC);
CREATE INDEX idx_incidents_rule ON alert_incidents(rule_id,state);

CREATE TABLE notification_deliveries (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    incident_id TEXT NOT NULL REFERENCES alert_incidents(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL,
    channel_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    notification_type TEXT NOT NULL CHECK(notification_type IN ('firing','resolved')),
    idempotency_key TEXT NOT NULL UNIQUE,
    payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','inflight','delivered','failed','canceled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 5),
    available_at_ms INTEGER NOT NULL,
    lease_token TEXT,
    lease_until_ms INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT,
    error_class TEXT NOT NULL DEFAULT '',
    CHECK((status='inflight' AND lease_token IS NOT NULL AND lease_until_ms IS NOT NULL) OR (status!='inflight' AND lease_token IS NULL AND lease_until_ms IS NULL)),
    CHECK((status IN ('delivered','failed','canceled'))=(finished_at IS NOT NULL))
);
CREATE INDEX idx_deliveries_due ON notification_deliveries(status,available_at_ms,seq);
CREATE INDEX idx_deliveries_incident ON notification_deliveries(incident_id,seq DESC);
CREATE INDEX idx_deliveries_lease ON notification_deliveries(status,lease_until_ms);

CREATE TABLE delivery_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id TEXT NOT NULL REFERENCES notification_deliveries(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK(attempt_number BETWEEN 1 AND 5),
    lease_token TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    outcome TEXT NOT NULL CHECK(outcome IN ('started','delivered','failed','unknown','canceled')),
    error_class TEXT NOT NULL DEFAULT '',
    UNIQUE(delivery_id,attempt_number),
    CHECK((outcome='started')=(completed_at IS NULL))
);

-- Existing rules recovered immediately. New rule defaults may inherit the trigger
-- duration, but migration explicitly retains the old behavior.
UPDATE alert_rules SET config_json=json_set(config_json,'$.recovery_seconds',0)
WHERE json_type(config_json,'$.recovery_seconds') IS NULL;

INSERT INTO alert_incidents(
    id,fingerprint,rule_id,node_id,node_name,kind,rule_snapshot_json,state,
    started_at,fired_at,resolved_at,observed_at,updated_at,trigger_since,
    last_message,resolution_reason,origin,approximate_start,last_enqueued_at,notification_sequence
)
SELECT lower(hex(randomblob(16))),s.fingerprint,r.id,n.id,n.name,r.kind,
    json_object('node_id',r.node_id,'channel_id',r.channel_id,'kind',r.kind,'config',json(r.config_json),'cooldown_seconds',r.cooldown_seconds),
    CASE WHEN r.enabled=0 THEN 'resolved' WHEN s.active=1 THEN 'firing' WHEN s.pending_since IS NOT NULL THEN 'pending' ELSE 'resolved' END,
    COALESCE(s.pending_since,s.last_attempt_at,s.updated_at),
    CASE WHEN s.active=1 THEN COALESCE(s.last_delivered_at,s.last_attempt_at,s.updated_at) ELSE NULL END,
    CASE WHEN r.enabled=0 OR (s.active=0 AND s.pending_since IS NULL) THEN s.updated_at ELSE NULL END,
    s.updated_at,s.updated_at,
    CASE WHEN s.active=0 AND s.pending_since IS NOT NULL AND r.enabled=1 THEN s.pending_since ELSE NULL END,
    s.last_message,
    CASE WHEN r.enabled=0 THEN 'rule_disabled' WHEN s.active=0 AND s.pending_since IS NULL THEN 'legacy_recovery' ELSE '' END,
    'legacy',1,
    CASE WHEN s.active=1 OR s.last_error<>'' THEN COALESCE(s.last_delivered_at,s.last_attempt_at) ELSE NULL END,
    CASE WHEN s.active=1 OR s.last_error<>'' THEN 1 ELSE 0 END
FROM alert_states s JOIN alert_rules r ON r.id=s.rule_id JOIN nodes n ON n.id=s.node_id
WHERE s.active=1 OR s.pending_since IS NOT NULL OR s.last_error<>'';

-- Successful old sends are not put back in the queue. A previously failed send
-- becomes an existing first attempt with the original cooldown, not a fresh send.
INSERT INTO notification_deliveries(
    id,incident_id,channel_id,channel_name,provider,notification_type,idempotency_key,
    payload_json,status,attempt_count,available_at_ms,created_at,updated_at,error_class
)
SELECT lower(hex(randomblob(16))),i.id,c.id,c.name,COALESCE(NULLIF(c.provider,''),c.kind),
    CASE WHEN s.active=1 THEN 'firing' ELSE 'resolved' END,
    'legacy:'||i.id||':'||c.id,
    json_object('title',CASE WHEN s.active=1 THEN 'MyProbe 告警' ELSE 'MyProbe 告警恢复' END,
      'message',s.last_message,'state',CASE WHEN s.active=1 THEN 'firing' ELSE 'resolved' END,
      'kind',r.kind,'node_id',i.node_id,'node_name',i.node_name,'rule_id',r.id,
      'incident_id',i.id,'timestamp',s.last_attempt_at),
    'pending',1,
    CAST(strftime('%s',s.last_attempt_at) AS INTEGER)*1000+MAX(r.cooldown_seconds,30)*1000,
    s.last_attempt_at,s.updated_at,'legacy_delivery_failed'
FROM alert_incidents i JOIN alert_states s ON s.fingerprint=i.fingerprint
JOIN alert_rules r ON r.id=i.rule_id JOIN notification_channels c ON c.id=r.channel_id
WHERE i.origin='legacy' AND s.last_error<>'' AND r.enabled=1;

INSERT INTO delivery_attempts(delivery_id,attempt_number,lease_token,started_at,completed_at,outcome,error_class)
SELECT id,1,'legacy',created_at,created_at,'failed','legacy_delivery_failed'
FROM notification_deliveries WHERE error_class='legacy_delivery_failed';
