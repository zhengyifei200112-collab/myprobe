-- Configuration identity changes only when managed sampling periods change.
ALTER TABLE nodes ADD COLUMN agent_config_revision INTEGER NOT NULL DEFAULT 1
    CHECK(typeof(agent_config_revision)='integer' AND agent_config_revision>0);
CREATE TRIGGER node_agent_config_revision AFTER UPDATE OF collection_seconds,report_seconds ON nodes
WHEN OLD.collection_seconds<>NEW.collection_seconds OR OLD.report_seconds<>NEW.report_seconds
BEGIN
    UPDATE nodes SET agent_config_revision=OLD.agent_config_revision+1 WHERE id=NEW.id;
END;

-- Ingress evidence belongs to the same transaction as the accepted report.
-- Unknown is explicit for internal writes without a transport source.
CREATE TABLE node_report_ingress (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    transport TEXT NOT NULL CHECK(transport IN ('unknown','http','websocket')),
    wire_version INTEGER,
    observed_at TEXT NOT NULL,
    captured_at TEXT NOT NULL,
    CHECK((transport='unknown' AND wire_version IS NULL) OR
          (transport IN ('http','websocket') AND wire_version IS NOT NULL AND wire_version IN (1,2)))
);

-- Epoch/nonce fields are internal fences, never public diagnostic identifiers.
CREATE TABLE node_configuration_evidence (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    connection_epoch TEXT NOT NULL,
    wire_version INTEGER NOT NULL CHECK(wire_version IN (1,2)),
    acknowledgement_supported INTEGER NOT NULL CHECK(acknowledgement_supported IN (0,1)),
    connected_at TEXT NOT NULL,
    issued_nonce TEXT,
    issued_revision INTEGER,
    dispatch_state TEXT NOT NULL DEFAULT 'none' CHECK(dispatch_state IN ('none','prepared','sent','failed')),
    dispatched_at TEXT,
    applied_revision INTEGER,
    applied_collection_seconds INTEGER,
    applied_report_seconds INTEGER,
    acknowledged_at TEXT,
    CHECK((issued_nonce IS NULL)=(issued_revision IS NULL)),
    CHECK((applied_revision IS NULL)=(acknowledged_at IS NULL))
);
