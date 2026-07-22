CREATE TABLE IF NOT EXISTS node_agent_metadata (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    hostname TEXT NOT NULL DEFAULT '',
    operating_system TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    platform_version TEXT NOT NULL DEFAULT '',
    kernel_version TEXT NOT NULL DEFAULT '',
    architecture TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL
);
