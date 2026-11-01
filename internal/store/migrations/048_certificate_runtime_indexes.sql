-- Permanent identities stay intact. Runtime queries visit current worksets;
-- historical skipped/disabled rows remain available to exact dedup and admins.
CREATE INDEX idx_alert_rules_enabled_created ON alert_rules(created_at) WHERE enabled=1;
CREATE INDEX idx_certificate_reminders_unskipped_service ON http_certificate_reminders(service_id,seq,rule_id) WHERE skipped=0;
