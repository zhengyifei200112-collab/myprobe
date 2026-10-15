-- Coverage is knowable only after this boundary. Do not infer past plans from
-- today's assignments or intervals when upgrading an existing installation.
CREATE TABLE network_schedule_metadata (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    known_since INTEGER NOT NULL
);
INSERT INTO network_schedule_metadata VALUES(1,unixepoch()+1);

CREATE TABLE network_schedule_epochs (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('ping','tcping')),
    host TEXT NOT NULL,
    port INTEGER,
    interval_seconds INTEGER NOT NULL CHECK(interval_seconds BETWEEN 5 AND 86400),
    timeout_ms INTEGER NOT NULL CHECK(timeout_ms BETWEEN 100 AND 60000),
    starts_at INTEGER NOT NULL,
    ends_at INTEGER CHECK(ends_at IS NULL OR ends_at>=starts_at)
);
CREATE UNIQUE INDEX idx_network_epoch_active_pair
    ON network_schedule_epochs(node_id,target_id) WHERE ends_at IS NULL;
CREATE INDEX idx_network_epoch_pair_start
    ON network_schedule_epochs(node_id,target_id,starts_at);
CREATE INDEX idx_network_epoch_pair_end
    ON network_schedule_epochs(node_id,target_id,ends_at);

INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at)
SELECT lower(hex(randomblob(16))),nt.node_id,t.id,t.kind,t.host,t.port,t.interval_seconds,t.timeout_ms,
       (SELECT known_since FROM network_schedule_metadata WHERE singleton=1)
FROM node_targets nt JOIN targets t ON t.id=nt.target_id WHERE t.enabled=1;

-- These hooks run in the configuration write transaction, including effective
-- assignments introduced by target groups and imports. Unrelated presentation
-- changes cannot reset an execution plan's phase.
CREATE TRIGGER network_assignment_added AFTER INSERT ON node_targets
BEGIN
    INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at)
    SELECT lower(hex(randomblob(16))),NEW.node_id,t.id,t.kind,t.host,t.port,t.interval_seconds,t.timeout_ms,
           max(unixepoch()+1,
               (SELECT known_since FROM network_schedule_metadata WHERE singleton=1),
               coalesce((SELECT max(ends_at) FROM network_schedule_epochs WHERE node_id=NEW.node_id AND target_id=NEW.target_id),0))
    FROM targets t WHERE t.id=NEW.target_id AND t.enabled=1;
END;

CREATE TRIGGER network_assignment_removed AFTER DELETE ON node_targets
BEGIN
    UPDATE network_schedule_epochs SET ends_at=max(starts_at,unixepoch()+1)
    WHERE node_id=OLD.node_id AND target_id=OLD.target_id AND ends_at IS NULL;
END;

CREATE TRIGGER network_target_definition_changed
AFTER UPDATE OF kind,host,port,interval_seconds,timeout_ms,enabled ON targets
WHEN OLD.kind IS NOT NEW.kind OR OLD.host IS NOT NEW.host OR OLD.port IS NOT NEW.port
  OR OLD.interval_seconds IS NOT NEW.interval_seconds OR OLD.timeout_ms IS NOT NEW.timeout_ms
  OR OLD.enabled IS NOT NEW.enabled
BEGIN
    UPDATE network_schedule_epochs SET ends_at=max(starts_at,unixepoch()+1)
    WHERE target_id=NEW.id AND ends_at IS NULL;
    INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at)
    SELECT lower(hex(randomblob(16))),nt.node_id,NEW.id,NEW.kind,NEW.host,NEW.port,NEW.interval_seconds,NEW.timeout_ms,
           max(unixepoch()+1,
               (SELECT known_since FROM network_schedule_metadata WHERE singleton=1),
               coalesce((SELECT max(ends_at) FROM network_schedule_epochs WHERE node_id=nt.node_id AND target_id=NEW.id),0))
    FROM node_targets nt WHERE nt.target_id=NEW.id AND NEW.enabled=1;
END;
