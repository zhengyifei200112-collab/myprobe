CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    csrf_token TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    hidden INTEGER NOT NULL DEFAULT 0 CHECK(hidden IN (0, 1)),
    tags_json TEXT NOT NULL DEFAULT '[]',
    country_code TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    price_minor INTEGER,
    billing_cycle TEXT NOT NULL DEFAULT '',
    expires_at TEXT,
    traffic_reset_day INTEGER CHECK(traffic_reset_day IS NULL OR (traffic_reset_day BETWEEN 1 AND 31)),
    use_since_boot INTEGER NOT NULL DEFAULT 0 CHECK(use_since_boot IN (0, 1)),
    latency_mode TEXT NOT NULL DEFAULT 'ping' CHECK(latency_mode IN ('ping', 'tcping')),
    custom_html TEXT NOT NULL DEFAULT '',
    collection_seconds INTEGER NOT NULL DEFAULT 5 CHECK(collection_seconds BETWEEN 1 AND 3600),
    report_seconds INTEGER NOT NULL DEFAULT 5 CHECK(report_seconds BETWEEN 1 AND 3600),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_seen_at TEXT
);

CREATE TABLE IF NOT EXISTS agent_tokens (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL DEFAULT 'primary',
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_agent_tokens_node_id ON agent_tokens(node_id);

CREATE TABLE IF NOT EXISTS metric_latest (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    captured_at TEXT NOT NULL,
    report_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS metric_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    captured_at TEXT NOT NULL,
    cpu_usage REAL NOT NULL,
    memory_used INTEGER NOT NULL,
    memory_total INTEGER NOT NULL,
    disk_used INTEGER NOT NULL,
    disk_total INTEGER NOT NULL,
    net_rx_rate REAL NOT NULL,
    net_tx_rate REAL NOT NULL,
    net_rx_total INTEGER NOT NULL,
    net_tx_total INTEGER NOT NULL,
    report_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metric_samples_node_time ON metric_samples(node_id, captured_at);

CREATE TABLE IF NOT EXISTS targets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('ping', 'tcping')),
    host TEXT NOT NULL,
    port INTEGER CHECK(port IS NULL OR (port BETWEEN 1 AND 65535)),
    interval_seconds INTEGER NOT NULL DEFAULT 60 CHECK(interval_seconds BETWEEN 5 AND 86400),
    timeout_ms INTEGER NOT NULL DEFAULT 5000 CHECK(timeout_ms BETWEEN 100 AND 60000),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS target_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('ping', 'tcping')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS target_group_members (
    group_id TEXT NOT NULL REFERENCES target_groups(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    PRIMARY KEY(group_id, target_id)
);

CREATE TABLE IF NOT EXISTS node_target_groups (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    group_id TEXT NOT NULL REFERENCES target_groups(id) ON DELETE CASCADE,
    PRIMARY KEY(node_id, group_id)
);

CREATE TABLE IF NOT EXISTS latency_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('ping', 'tcping')),
    captured_at TEXT NOT NULL,
    success INTEGER NOT NULL CHECK(success IN (0, 1)),
    latency_ms REAL,
    error_class TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_latency_node_target_time ON latency_samples(node_id, target_id, captured_at);

CREATE TABLE IF NOT EXISTS notification_channels (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('telegram', 'webhook')),
    config_encrypted TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_rules (
    id TEXT PRIMARY KEY,
    node_id TEXT REFERENCES nodes(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('offline', 'cpu', 'bandwidth', 'cycle_traffic', 'expiry')),
    config_json TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    cooldown_seconds INTEGER NOT NULL DEFAULT 900,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_events (
    id TEXT PRIMARY KEY,
    rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    node_id TEXT REFERENCES nodes(id) ON DELETE SET NULL,
    state TEXT NOT NULL CHECK(state IN ('firing', 'resolved', 'failed')),
    fingerprint TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at TEXT NOT NULL,
    delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_alert_events_fingerprint ON alert_events(fingerprint, created_at);

CREATE TABLE IF NOT EXISTS chart_shares (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    node_filter_json TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL DEFAULT '',
    remote_ip TEXT NOT NULL DEFAULT '',
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);
