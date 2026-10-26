-- Record actual transactional decisions; do not infer/backfill historical causes
-- from time overlap. Keep distinct observation-gap and delivery decisions.
CREATE TABLE incident_maintenance_decisions (
    incident_id TEXT NOT NULL REFERENCES alert_incidents(id) ON DELETE CASCADE,
    window_id TEXT NOT NULL REFERENCES maintenance_windows(id),
    kind TEXT NOT NULL CHECK(kind IN ('observation','observation_gap','delivery')),
    first_at_ms INTEGER NOT NULL,
    last_at_ms INTEGER NOT NULL CHECK(last_at_ms >= first_at_ms),
    PRIMARY KEY(incident_id,window_id,kind)
);
CREATE TABLE delivery_maintenance_links (
    delivery_id TEXT NOT NULL REFERENCES notification_deliveries(id) ON DELETE CASCADE,
    window_id TEXT NOT NULL REFERENCES maintenance_windows(id),
    recorded_at_ms INTEGER NOT NULL,
    PRIMARY KEY(delivery_id,window_id)
);
