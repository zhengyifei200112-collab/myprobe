-- Exact UTC nanosecond keys preserve ordering across optional RFC3339 fractions.
-- Original evidence timestamps and payloads remain unchanged.
ALTER TABLE alert_incidents ADD COLUMN started_sort_ns TEXT GENERATED ALWAYS AS (
 substr(started_at,1,19)||'.'||substr(CASE WHEN substr(started_at,20,1)='.' THEN substr(started_at,21,length(started_at)-21) ELSE '' END||'000000000',1,9)||'Z') VIRTUAL;
ALTER TABLE alert_incidents ADD COLUMN fired_sort_ns TEXT GENERATED ALWAYS AS (
 CASE WHEN fired_at IS NOT NULL THEN substr(fired_at,1,19)||'.'||substr(CASE WHEN substr(fired_at,20,1)='.' THEN substr(fired_at,21,length(fired_at)-21) ELSE '' END||'000000000',1,9)||'Z' END) VIRTUAL;
ALTER TABLE alert_incidents ADD COLUMN resolved_sort_ns TEXT GENERATED ALWAYS AS (
 CASE WHEN resolved_at IS NOT NULL THEN substr(resolved_at,1,19)||'.'||substr(CASE WHEN substr(resolved_at,20,1)='.' THEN substr(resolved_at,21,length(resolved_at)-21) ELSE '' END||'000000000',1,9)||'Z' END) VIRTUAL;
ALTER TABLE audit_log ADD COLUMN created_sort_ns TEXT GENERATED ALWAYS AS (
 substr(created_at,1,19)||'.'||substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END||'000000000',1,9)||'Z') VIRTUAL;
CREATE INDEX idx_incidents_node_started_ns ON alert_incidents(node_id,started_sort_ns);
CREATE INDEX idx_incidents_node_fired_ns ON alert_incidents(node_id,fired_sort_ns);
CREATE INDEX idx_incidents_node_resolved_ns ON alert_incidents(node_id,resolved_sort_ns);
CREATE INDEX idx_audit_object_time_ns ON audit_log(object_type,object_id,created_sort_ns);
CREATE INDEX idx_audit_action_time_ns ON audit_log(object_type,action,created_sort_ns);
