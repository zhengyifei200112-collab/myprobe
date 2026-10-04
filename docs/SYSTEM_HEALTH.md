# System health and node diagnostics (SYS-01)

Status: implementation in progress on a branch from current main. Not released.

## Current API contract

`GET /api/v1/admin/system/health` requires an active administrator session.
Both successful and rejected requests use `Cache-Control: private, no-store`.
The response currently contains `observed_at`, `database`, `retention`,
`transport`, `browser_subscriptions`, and `notification_queue`.

Database inspection failures are represented by `database.status=unavailable`
and a fixed error code, without raw errors or paths. Missing file sizes and
unknown file sizes remain distinct. Retention observations have process scope;
`never_run` does not establish that earlier processes never ran retention.
Transport counts are sampled under separate locks, not a transaction spanning
all connections. Pending results expire at their deadline; diagnostics do not
remove expired entries. Browser subscriptions are separate from Agent sockets.
The notification queue reports `unavailable` with reason
`durable_outbox_not_integrated`; it does not expose an invented zero count.

The `scheduler` field observes the actual running scheduler supplied by the
Server entry point. Without an attached observer it explicitly reports unavailable.
Its process-local job records never-run, running, success, failure and cancellation.
`last_cycle` includes scheduled time, start delay, assignment-read success, due,
dispatched, offline and failed counts. A failed assignment read does not establish
zero configured tasks. Offline Agents do not make a completed dispatch cycle fail;
other dispatch errors do. These are last-cycle counts, not durable queue totals.
The last completed cycle remains visible while the next cycle is running.

`retention.configuration` reports effective raw/minute/five-minute retention and
run interval in seconds, displayed as days/hours. Missing configuration is
unavailable. `notification_engine` observes the actual running alert service,
including disabled encryption configuration, loop lifecycle and evaluation job
outcomes. Rule-level errors mark that observation failed even when the legacy
evaluation loop continues processing other rules. Raw error messages and key
material are excluded. Engine status does not prove delivery or outbox health.

The `server` field reports the version supplied by the build and the start time
captured on entry to `main`, with uptime calculated using Go's monotonic clock
when available. Docker and release binaries inject the same release version into
Server and Agent; ordinary source builds report `dev`. The field also includes Go
version, OS and architecture. Missing identity has explicit unavailable statuses;
it is not replaced by the API construction time or a guessed release version.
This does not check for newer upstream releases or establish Agent compatibility.

The `backup` field observes actual encrypted file generation after request and
storage eligibility validation. Successful generation is recorded after encryption,
file sync and rewind, before streaming the response. Invalid input is not a run;
failed generation and cancelled requests are distinct. `download_saved` and
`recovery_verified` remain `unknown`, including after a successful generation.
These observations reset on restart and do not claim historical backup coverage.

`GET /api/v1/admin/nodes/:nodeID/diagnostics` uses the same private administrator
boundary. It performs one indexed node/metadata lookup and returns recent report
time, configured reporting interval, advertised Agent version/capabilities and
the Server receipt time of the last hello. It excludes hostname, machine ID,
addresses and credentials. No hello is explicitly unavailable, not an empty
capability claim; a hello is not evidence of a live connection. Current WebSocket
presence is sampled separately. Last-report transport is unknown and configuration
acknowledgement remains unavailable until that provider is integrated. The
advertised metadata can be older than the current session; its timestamp matters.
Missing nodes return 404 only after administrator authentication.

The management navigation now includes a system health page with manual refresh,
last observation time, process-local job outcomes, database/transport counts and
last scheduler cycle. A failed refresh marks retained values as stale; unauthorized
responses clear data and return to login. Unmounting aborts in-flight requests.

The health tab uses `/admin?tab=health`; selecting a node adds `diagnostic_node`.
Reload and browser Back restore this location, including after password login.
These query values select the UI only; the administrator API independently checks
the session and node existence. OAuth response cleanup preserves unrelated query
parameters. Live browser acceptance covers refresh and Back restoration.
The page includes a node selector and independent refresh. Switching nodes clears
old results and cancels prior requests, with a generation guard against stale
responses. `scripts/system-health-ui-acceptance.cjs` exercises synthetic API data
in light/dark at 360/768/1440, node selection, failed-refresh notices, retry and
401 clearing. These component checks passed. The companion
`scripts/system-health-live-acceptance.cjs` passed against a disposable file-backed
Server built with version `health-live-acceptance`: real password login, navigation,
actual retention/scheduler observations, fixture-node lookup, keyboard activation
of navigation and refresh, six light/dark viewport combinations without horizontal
overflow, and revoked-session login fallback. It deletes its fixture node and does
not use production data or notification channels. This checks keyboard activation,
not a complete screen-reader or accessibility audit.

Linux CI run `37141705827` passed for implementation commit `3db292d`; local
`go vet ./...` and `go build ./cmd/...` also passed. Configuration acknowledgement
and notification-outbox integration remain pending.

Additional verification: Linux CI and governance checks passed at `c9e64c6`.
The actual WebSocket lifecycle test covers unregistered sockets, valid hello,
replacement/old-session teardown, dispatched pending results and abrupt disconnect.
Local full Go tests pass except the existing Windows collector path assertion
`TestDiskUsagePathUsesHostRootForAbsoluteMounts` (handled by separate PR #47).
Full local Go vet/build pass. The new lifecycle test does not contact any probe
target; it verifies the task frame on a loopback WebSocket.
This branch does not complete SYS-01. Contract
tests cover authentication, revoked sessions, cache policy, evidence semantics
and secret exclusion; store/gateway tests cover underlying observations.

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
