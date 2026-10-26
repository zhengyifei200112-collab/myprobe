-- Retain the actor and confirmed revision independently of user lifecycle.
-- audit_log supplies the existing authenticated-user foreign-key check.
CREATE TABLE maintenance_cancel_actions (
    window_id TEXT PRIMARY KEY REFERENCES maintenance_windows(id),
    actor TEXT NOT NULL,
    confirmed_revision INTEGER NOT NULL CHECK(confirmed_revision > 0)
);
