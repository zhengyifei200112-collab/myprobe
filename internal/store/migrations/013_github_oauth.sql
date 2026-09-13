CREATE TABLE IF NOT EXISTS github_oauth_settings (
    id INTEGER PRIMARY KEY CHECK(id=1),
    enabled INTEGER NOT NULL DEFAULT 0,
    client_id TEXT NOT NULL DEFAULT '',
    client_secret_encrypted TEXT NOT NULL DEFAULT '',
    callback_url TEXT NOT NULL DEFAULT '',
    username_allowlist_json TEXT NOT NULL DEFAULT '[]',
    verified_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO github_oauth_settings(id,created_at,updated_at) VALUES(1,datetime('now'),datetime('now'));

CREATE TABLE IF NOT EXISTS oauth_states (
    state_hash TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_oauth_states_expiry ON oauth_states(expires_at);
