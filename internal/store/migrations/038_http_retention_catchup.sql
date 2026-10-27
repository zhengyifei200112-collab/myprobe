CREATE INDEX idx_http_epochs_retention ON http_schedule_epochs(end_ns)
WHERE end_ns IS NOT NULL;
