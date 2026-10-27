CREATE INDEX idx_http_diagnostics_page ON http_tasks(service_id,node_id,
 substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29) DESC,id DESC);
