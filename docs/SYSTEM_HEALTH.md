# System health and node diagnostics (SYS-01)

Status: implementation in progress on a branch from current main. Not released.

## User outcome

An administrator can determine whether the monitor itself is working: Server
version/uptime, SQLite and WAL size, retention configuration and last run, Agent
WebSocket connections, browser subscriptions, pending probe results, scheduler
progress, notification delivery capability and last backup generation result.
Node diagnostics explain the last report, advertised Agent version/capabilities
and available configuration acknowledgement evidence.

## Evidence and scope

- Only authenticated, non-cacheable administrator APIs expose diagnostics. No
  filesystem paths, raw IPs, credentials, report payloads or raw errors are returned.
- Task observations are process-local and explicitly reset on restart. Never-run,
  running, successful, failed and cancelled are distinct. Missing evidence is not zero.
- SQLite inspection uses metadata and file stat calls; no checkpoint, VACUUM,
  full integrity scan or sample-table count is triggered by page refresh.
- Connected Agent WebSockets, browser subscriptions and outstanding probe results
  are separate quantities. HTTP reports are not persistent socket connections.
- Scheduler lateness and currently outstanding results are separate from a durable
  queue. Queue statistics are exposed only when that implementation is available.
- Current main has no durable notification outbox (PR #50 supplies it). Its absence
  must be explicit; integrating that provider remains part of the roadmap, not an
  invented zero-length queue.
- A successfully generated encrypted backup does not establish that the browser
  saved it or that recovery was verified. SYS-02 owns scheduled backups/recovery.
- Version strings are reported as supplied by build/Agent evidence. Do not infer
  protocol incompatibility or claim a latest upstream release from version ordering.
- No remote commands, automatic update execution or public diagnostic export.

## Delivery and acceptance

Implement evidence collection first, then a bounded `/api/v1/admin/system/health`
response and the management page. Add diagnostics for node capabilities/configuration
only where the Agent handshake or acknowledgement provides evidence; unavailable
fields must carry an explicit status. Preserve the existing public `/healthz` contract.

Test concurrent task observation, cancellation, restart/never-run state, file-backed
and memory databases, inaccessible files, authentication, secret exclusion, real
socket lifecycle, probe expiry and actual retention/backup outcomes. Browser checks
cover light/dark at 360/768/1440, refresh/error/retry, stale data and keyboard access.
Regenerate embedded assets, run repository checks, and record current-head CI before
review readiness. No schema or Agent protocol change is planned for this task.
