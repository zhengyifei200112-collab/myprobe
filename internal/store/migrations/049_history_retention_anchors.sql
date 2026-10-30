-- Only the small set of retained counter anchors needs a work marker. Do not
-- backfill or build a new index over the existing raw history during startup.
CREATE TABLE history_retention_anchors (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    sample_id INTEGER UNIQUE REFERENCES metric_samples(id) ON DELETE SET NULL
);
