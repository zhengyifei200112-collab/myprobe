-- Requested settings are not observed execution evidence. Old results remain
-- v1, without a family or ICMP packet measurement; do not reconstruct them.
ALTER TABLE targets ADD COLUMN address_family TEXT NOT NULL DEFAULT 'auto' CHECK(address_family IN ('auto','ipv4','ipv6'));
ALTER TABLE targets ADD COLUMN packet_count INTEGER NOT NULL DEFAULT 1 CHECK(packet_count BETWEEN 0 AND 5);
UPDATE targets SET packet_count=0 WHERE kind='tcping';
ALTER TABLE network_schedule_epochs ADD COLUMN address_family TEXT NOT NULL DEFAULT 'auto' CHECK(address_family IN ('auto','ipv4','ipv6'));
ALTER TABLE network_schedule_epochs ADD COLUMN packet_count INTEGER NOT NULL DEFAULT 1 CHECK(packet_count BETWEEN 0 AND 5);
UPDATE network_schedule_epochs SET packet_count=0 WHERE kind='tcping';
ALTER TABLE network_probe_tasks ADD COLUMN address_family TEXT NOT NULL DEFAULT 'auto' CHECK(address_family IN ('auto','ipv4','ipv6'));
ALTER TABLE network_probe_tasks ADD COLUMN packet_count INTEGER NOT NULL DEFAULT 1 CHECK(packet_count BETWEEN 0 AND 5);
UPDATE network_probe_tasks SET packet_count=0 WHERE kind='tcping';
ALTER TABLE network_probe_tasks ADD COLUMN result_format TEXT NOT NULL DEFAULT 'v1' CHECK(result_format IN ('v1','network.v1','dispatch.v1'));
ALTER TABLE network_probe_tasks ADD COLUMN evidence_required INTEGER NOT NULL DEFAULT 0 CHECK(evidence_required IN (0,1));

DROP TRIGGER network_assignment_added;
CREATE TRIGGER network_assignment_added AFTER INSERT ON node_targets
BEGIN
    INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at,address_family,packet_count)
    SELECT lower(hex(randomblob(16))),NEW.node_id,t.id,t.kind,t.host,t.port,t.interval_seconds,t.timeout_ms,
           max(unixepoch()+1,
               (SELECT known_since FROM network_schedule_metadata WHERE singleton=1),
               coalesce((SELECT max(ends_at) FROM network_schedule_epochs WHERE node_id=NEW.node_id AND target_id=NEW.target_id),0)),
           t.address_family,t.packet_count
    FROM targets t WHERE t.id=NEW.target_id AND t.enabled=1;
END;

DROP TRIGGER network_target_definition_changed;
CREATE TRIGGER network_target_definition_changed
AFTER UPDATE OF kind,host,port,interval_seconds,timeout_ms,enabled,address_family,packet_count ON targets
WHEN OLD.kind IS NOT NEW.kind OR OLD.host IS NOT NEW.host OR OLD.port IS NOT NEW.port
  OR OLD.interval_seconds IS NOT NEW.interval_seconds OR OLD.timeout_ms IS NOT NEW.timeout_ms
  OR OLD.enabled IS NOT NEW.enabled OR OLD.address_family IS NOT NEW.address_family
  OR OLD.packet_count IS NOT NEW.packet_count
BEGIN
    UPDATE network_schedule_epochs SET ends_at=max(starts_at,unixepoch()+1)
    WHERE target_id=NEW.id AND ends_at IS NULL;
    INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at,address_family,packet_count)
    SELECT lower(hex(randomblob(16))),nt.node_id,NEW.id,NEW.kind,NEW.host,NEW.port,NEW.interval_seconds,NEW.timeout_ms,
           max(unixepoch()+1,
               (SELECT known_since FROM network_schedule_metadata WHERE singleton=1),
               coalesce((SELECT max(ends_at) FROM network_schedule_epochs WHERE node_id=nt.node_id AND target_id=NEW.id),0)),
           NEW.address_family,NEW.packet_count
    FROM node_targets nt WHERE nt.target_id=NEW.id AND NEW.enabled=1;
END;
