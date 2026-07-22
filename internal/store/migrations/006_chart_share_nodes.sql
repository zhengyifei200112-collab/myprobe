CREATE TABLE IF NOT EXISTS chart_share_nodes (
    share_id TEXT NOT NULL REFERENCES chart_shares(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY(share_id, node_id)
);

INSERT OR IGNORE INTO chart_share_nodes(share_id, node_id)
SELECT shares.id, json_each.value
FROM chart_shares AS shares, json_each(shares.node_filter_json)
JOIN nodes ON nodes.id = json_each.value;
