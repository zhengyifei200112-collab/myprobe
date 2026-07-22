CREATE TABLE IF NOT EXISTS chart_share_sessions (
    id TEXT PRIMARY KEY,
    share_id TEXT NOT NULL REFERENCES chart_shares(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chart_share_sessions_expiry ON chart_share_sessions(expires_at);

CREATE TABLE IF NOT EXISTS chart_share_login_attempts (
    attempt_key TEXT PRIMARY KEY,
    attempts INTEGER NOT NULL,
    window_started_at TEXT NOT NULL,
    blocked_until TEXT,
    updated_at TEXT NOT NULL
);
