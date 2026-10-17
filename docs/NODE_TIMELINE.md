# Node timeline and curve navigation (DETAIL-02)

Status: implementation on the #91 dependency stack; exact-source CI/review and
final integration acceptance are required. No production deployment or release.

The node page shows events in reverse chronological order. Selecting an event
marks its time on each available curve and zooms to a clipped interval around it.
Existing shared cursors, missing-data gaps, manual zoom and reset remain available.
The API retains source nanoseconds; chart positions use browser milliseconds.
The scrollable event list, empty/partial states, keyboard buttons and pagination
work in the same 360/768/1440 light/dark layouts as node history.

## Evidence and meaning

- `report_observed`: first retained observation available in the selected window
  without a retained predecessor. This is not the host's first boot/connection.
- `report_gap` / `report_resumed`: the observed capture interval exceeds
  `max(20, 3 * max(current collection_seconds, current report_seconds))` seconds.
  A trailing gap is estimated only up to the selected end. Historical interval
  settings are unavailable, so these are approximate observations, not proof of
  host downtime. Subsequent reports beyond the selected end do not affect them.
- `reboot_hint`: positive reported uptime decreased between ordered distinct
  captures. Unknown/zero uptime and Agent reconnects do not establish a reboot.
  Labels say “检测到重启迹象”, never a confirmed machine reboot.
- Private `incident_observed`, `incident_fired` and `incident_recovered` separate
  first observed abnormality, threshold crossing and confirmed recovery. Other
  end reasons use `incident_closed`, not a recovery claim. Legacy approximate
  start flags survive. Node-owned HTTP incidents use the same phases and link to
  their existing incident detail; unrelated/global subjects are not reassigned
  to a node from its current service membership.
- Private maintenance start/end/cancel use persisted historical node membership.
  Cancellation before start suppresses the nonexistent start/end; an active
  cancellation suppresses the planned end. Reasons/actors are not copied.
- Private single-node and node-member batch/reorder audit records show management
  saves. They do not prove the Agent applied a configuration. Raw audit payloads,
  before/after values, credentials, user notes and remote addresses are excluded.
  Changes without a historical node-attributed audit receipt are not invented.

Observation events depend on retained raw reports. Rollups cannot reconstruct
deleted captures/uptimes; `observation_start` reports the first checked capture
inside this query. A retained predecessor may still explain a gap/reboot at the
left edge. No events does not establish continuous uptime. Each request examines
at most the newest 100000 raw rows plus one predecessor, including legacy duplicate
rows, and keeps at most `limit+1` events in a heap. `observations_truncated=true`
explicitly asks users to narrow a window when that work budget is exceeded.
It is independent from event-page truncation and database retention settings.

## API, privacy and pagination

`GET /api/v1/public/nodes/:id/events` and authenticated
`GET /api/v1/admin/nodes/:id/events` accept the existing preset `range` or paired
RFC3339 `start`/`end` selectors, at most 365 days, with `[start,end)` semantics.
The page loads history first, then uses its fixed bounds for events. Optional
`limit` is 1–100 (default 50). Response fields are `events`, `start`, `end`,
`next_cursor`, `observation_start`, `observations_truncated` and
`gap_threshold_seconds`. Events contain only `id`, `time`, fixed `kind`,
`approximate` and optional private `incident_id`.

For another page, send the returned fixed `start`/`end` and `next_cursor` as
`cursor`. A preset plus cursor is rejected because its moving end is ambiguous.
The cursor binds node, public/private scope, bounds and the last `(time,id)`.
Equal-time events have deterministic ID ordering. Current authorization always
runs; cursor encoding is navigation state, not a signed permission grant.
Concurrent late inserts/configuration edits can change later reads; refresh the
fixed window for a fresh view rather than treating a cursor as a snapshot lease.

One read transaction checks current node visibility and reads all sources.
Public hidden/missing IDs share 404, even with administrator cookies. Public
events have no incident IDs, audit records, maintenance or private payloads.
Shares gain no new event endpoint or scope. Responses are `no-store`; pagination
and range parsing are bounded, repeated selectors rejected, and query work has a
10-second request deadline. A revoked session or hidden node clears charts and
timeline before relogin/retry; stale responses cannot revive private content.

## Migration and operations

Forward-only migration `047_node_timeline_indexes.sql` adds virtual, exact UTC
nanosecond time keys and node/object/time indexes to incidents and audit logs.
Original timestamp/payload bytes remain unchanged. Maintenance uses its existing
node-membership index; raw observations use migration 041's node/time index.
No Agent protocol/configuration change or new notification delivery occurs.

Before an upgrade, create and verify a consistent backup and preserve its original
encryption key, runtime configuration and exact previous image/binary privately.
Index creation can take time on large incident/audit tables. Start one new Server
against the backed-up database, then verify migration receipts and current
authorization/Agent ingestion. Rollback restores the pre-upgrade database plus
matching key/configuration and previous executable; do not run an older version
against an already migrated live database. No downgrade migration is provided.

## Validation

Store/API regressions cover public projection, hidden/missing/session/share
boundaries, slow collection, exact fractional edges, predecessor/gap/reboot
meaning, equal-time paging, cross-scope cursor rejection, malformed legacy audit
arrays, source indexes, bounded duplicate work, forward migration receipt failure
rollback, preserved raw evidence and idempotent reopen.

Run the repository baseline and the existing `node-details` disposable-Server QA
profile documented in [node details](NODE_DETAILS.md). It now includes forty
actual accepted reports with a decreasing uptime, fifty-five private configuration
updates, real cursor paging, event zoom/marker pixel changes, public hiding and
private session revocation, six real administrator timeline views and six
synthetic public views. Other form/discovery/order/batch and network-comparison
checks remain required where the shared charting bundle changes. Synthetic or
local OAuth continuation does not validate external GitHub authorization. No
Provider messages or production credentials belong to these fixtures.

The running native Oracle soak remains frozen at its original 6ad1137 source;
it cannot establish the final integrated timeline source's capacity/24h result.
