ALTER TABLE nodes ADD COLUMN config_revision INTEGER NOT NULL DEFAULT 0;

-- Reports/heartbeats do not invalidate previews; every configuration write does.
CREATE TRIGGER nodes_config_revision AFTER UPDATE OF
name,sort_order,hidden,tags_json,country_code,currency,price_minor,billing_cycle,
expires_at,traffic_reset_day,use_since_boot,latency_mode,custom_html,
custom_badges_json,custom_links_json,collection_seconds,report_seconds ON nodes
BEGIN
    UPDATE nodes SET config_revision=config_revision+1 WHERE id=NEW.id;
END;
CREATE TRIGGER node_targets_revision_insert AFTER INSERT ON node_targets
BEGIN
    UPDATE nodes SET config_revision=config_revision+1 WHERE id=NEW.node_id;
END;
CREATE TRIGGER node_targets_revision_delete AFTER DELETE ON node_targets
BEGIN
    UPDATE nodes SET config_revision=config_revision+1 WHERE id=OLD.node_id;
END;

CREATE TABLE node_batch_previews (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    preview_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    applied_at TEXT,
    idempotency_key TEXT,
    result_json TEXT,
    UNIQUE(user_id,idempotency_key)
);
CREATE INDEX node_batch_previews_expiry ON node_batch_previews(expires_at);
