CREATE TABLE IF NOT EXISTS node_targets (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    PRIMARY KEY(node_id, target_id)
);

-- Preserve the effective assignments created through target groups. The legacy
-- tables remain readable so existing configuration exports stay compatible.
INSERT OR IGNORE INTO node_targets(node_id, target_id)
SELECT DISTINCT ng.node_id, gm.target_id
FROM node_target_groups ng
JOIN target_group_members gm ON gm.group_id = ng.group_id;
