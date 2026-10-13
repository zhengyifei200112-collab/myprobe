-- Existing draft aggregates retain their original renderer/key. New groups use
-- version 2 and freeze the configured origin when their fixed window starts.
ALTER TABLE notification_aggregates ADD COLUMN render_version INTEGER NOT NULL DEFAULT 1 CHECK(render_version IN (1,2));
ALTER TABLE notification_aggregates ADD COLUMN notification_origin TEXT NOT NULL DEFAULT '';
