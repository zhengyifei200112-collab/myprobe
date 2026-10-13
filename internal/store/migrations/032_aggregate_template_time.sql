-- Capture the private template clock for new groups. Existing queued groups
-- retain the legacy timestamp rendering rather than rewriting their payload/key.
ALTER TABLE notification_aggregates ADD COLUMN template_event_time TEXT NOT NULL DEFAULT '' CHECK(length(template_event_time) IN (0,19));
