-- Keep original rule IDs so incident fingerprints and outbox payloads survive.
-- Rule IDs are snapshots: existing rule deletion must not silently erase history.
CREATE TABLE alert_policy_rule_bindings (
    policy_id TEXT NOT NULL REFERENCES alert_policies(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    rule_id TEXT NOT NULL UNIQUE,
    origin TEXT NOT NULL CHECK(origin IN ('legacy','materialized')),
    PRIMARY KEY(policy_id,node_id)
);
