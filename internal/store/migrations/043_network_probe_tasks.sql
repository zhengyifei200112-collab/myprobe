-- Plan history can predate trustworthy result identities. Keep a separate
-- boundary so a later upgrade never labels old untracked results as missing.
ALTER TABLE network_schedule_metadata ADD COLUMN known_results_since INTEGER NOT NULL DEFAULT 0;
UPDATE network_schedule_metadata SET known_results_since=unixepoch()+1 WHERE singleton=1;

CREATE TABLE network_probe_tasks (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL CHECK(source IN ('scheduled','manual')),
    epoch_id TEXT REFERENCES network_schedule_epochs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    scheduled_at INTEGER,
    kind TEXT NOT NULL CHECK(kind IN ('ping','tcping')),
    host TEXT NOT NULL,
    port INTEGER,
    timeout_ms INTEGER NOT NULL CHECK(timeout_ms BETWEEN 100 AND 60000),
    dispatch_until_ns INTEGER NOT NULL,
    expires_at_ns INTEGER NOT NULL,
    created_at_ns INTEGER NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('ready','sending','sent','sent_unknown','completed')),
    lease_token TEXT NOT NULL,
    lease_until_ns INTEGER NOT NULL,
    retry_after_ns INTEGER NOT NULL DEFAULT 0,
    dispatch_reason TEXT NOT NULL DEFAULT '' CHECK(dispatch_reason IN ('','offline','write_error','dispatch_unknown')),
    sent_at_ns INTEGER,
    received_at_ns INTEGER,
    result_sha TEXT,
    result_json TEXT,
    outcome TEXT CHECK(outcome IN ('success','failure','not_executed')),
    UNIQUE(epoch_id,scheduled_at),
    CHECK((source='scheduled' AND epoch_id IS NOT NULL AND scheduled_at IS NOT NULL)
       OR (source='manual' AND epoch_id IS NULL AND scheduled_at IS NULL)),
    CHECK((state='completed' AND result_sha IS NOT NULL AND result_json IS NOT NULL AND outcome IS NOT NULL)
       OR (state<>'completed' AND result_sha IS NULL AND result_json IS NULL AND outcome IS NULL))
);
CREATE INDEX idx_network_tasks_node_expiry ON network_probe_tasks(node_id,expires_at_ns);
CREATE INDEX idx_network_tasks_retention ON network_probe_tasks(expires_at_ns);
CREATE INDEX idx_network_tasks_dispatch_lease ON network_probe_tasks(lease_until_ns,id) WHERE state='sending';
