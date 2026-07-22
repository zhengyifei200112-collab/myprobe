CREATE TABLE IF NOT EXISTS login_failures (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    remote_ip TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_failures_pair_time ON login_failures(username, remote_ip, created_at);
CREATE INDEX IF NOT EXISTS idx_login_failures_ip_time ON login_failures(remote_ip, created_at);

CREATE TABLE IF NOT EXISTS captcha_challenges (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    remote_ip TEXT NOT NULL,
    answer_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_captcha_expires_at ON captcha_challenges(expires_at);
