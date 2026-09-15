ALTER TABLE notification_channels ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_channels ADD COLUMN last_test_status TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_channels ADD COLUMN last_test_error TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_channels ADD COLUMN last_test_at TEXT;
ALTER TABLE alert_states ADD COLUMN pending_since TEXT;
UPDATE notification_channels SET provider=kind WHERE provider='';

CREATE TABLE IF NOT EXISTS notification_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    event_kind TEXT NOT NULL,
    title_template TEXT NOT NULL,
    body_template TEXT NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notification_templates_event_kind ON notification_templates(event_kind, name);
CREATE INDEX IF NOT EXISTS idx_alert_events_created_at ON alert_events(created_at DESC);

INSERT OR IGNORE INTO notification_templates(id,name,event_kind,title_template,body_template,is_default,created_at,updated_at) VALUES
('default-firing','默认告警','firing','MyProbe 告警 · {{node.name}}','{{message}}',1,datetime('now'),datetime('now')),
('default-resolved','默认恢复','resolved','MyProbe 恢复 · {{node.name}}','{{message}}',1,datetime('now'),datetime('now'));
