-- Keep selection provenance separately so legacy internally-created windows
-- remain readable without inventing their original selection.
CREATE TABLE maintenance_selections (
    window_id TEXT PRIMARY KEY REFERENCES maintenance_windows(id),
    selection_json TEXT NOT NULL
);
CREATE TABLE maintenance_create_requests (
    request_id TEXT PRIMARY KEY,
    request_digest TEXT NOT NULL,
    window_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);
