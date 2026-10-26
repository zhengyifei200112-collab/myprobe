# Alert maintenance implementation contract

Status: implementation in progress; management preview/create/cancel/list, browser
UI and notification suppression exist. Real browser and local-channel runtime
tests cover the main lifecycle; additional release integration remains necessary.
Roadmap task ALT-03. This branch depends on scoped-policy PR #54 and
uses it as a stacked base; it must not duplicate incident or policy state machines.

Storage foundation: migration 024 adds windows, frozen node membership and a writer
reservation. Create validates all member IDs atomically, at most 100 members and
1000 retained windows. Times normalize to UTC milliseconds; reason, creator and
IANA display timezone are bounded/validated. Cancellation is revision-guarded and
retains its timestamp, so historical queries before cancellation remain correct.
Node deletion preserves membership history. Tests cover start/end, overlapping
windows, stale/repeated cancel, deleted nodes, reopen and invalid-input rollback.
Historical interval queries also test whether an observation gap overlaps an
effective maintenance interval. They clip at cancellation, exclude windows canceled
before starting, and preserve exclusive-end semantics even for sub-millisecond
query boundaries. This is evidence of overlap only, not proof that a fault actually
started or recovered during an unobserved interval.
The observation transaction now checks active maintenance after state transitions,
suppresses new notifications, cancels outstanding jobs with a maintenance reason,
and resets the repeat clock. Tests verify in-window recovery remains recorded,
no catch-up for that recovery, fresh continuing faults notify once after end, and
unknown observations at end do not notify. Worker claim and pre-send checks now
cancel jobs under active maintenance in the same writer transaction as resetting
the incident repeat clock. Tests cover pending and already-leased jobs, explicit
maintenance cancellation reasons, no resurrection at end and a new notification
only after a fresh observation. A send already past the final check remains a race.
If cancellation wins while that sender is running, its logical job stays canceled
and cannot retry. The latest canceled attempt's original token may still record
a real success, rejection or transport uncertainty. That refinement neither
revives the job nor changes the incident repeat clock; a replay of the identical
result is harmless. A superseded token or conflicting result is rejected. Until
a result arrives, cancellation leaves the running attempt unknown. Process exit
may leave it unknown permanently; the implementation does not reconstruct a remote
result. Cancellation timestamps remain distinct from attempt-completion timestamps.
Claim and pre-send checks also inspect maintenance overlap since each delivery's
creation, preventing an old queued transition from sending after downtime spanning
an entire window. A close/reopen test verifies cancellation of the old job and
acceptance of a new post-window observation/job. A fresh continuing firing
observation also checks the gap since the persisted last known observation. If it
crossed maintenance, the same transaction resets the repeat clock, cancels old
outstanding intent and records the new observation. This reuses the observation
watermark instead of a separate suppression marker. Unknown observations leave
that watermark intact and do not enqueue a summary. Tests cover an already
delivered fault, restart across the window, unknown then fresh evidence, and a
second restart with no duplicate summary. Recovery first observed after a gap is
still dated at the observed recovery time; it is not assumed to have happened
inside maintenance. Store tests cover overlap and cancellation. Local-channel worker
tests verify zero notifications for a fault recovered inside maintenance and one
fresh summary for a continuing fault after API cancellation, without restart
duplicates. Tag resolution, selection provenance, audit actors and current-schema
backup/restore tests are implemented.

Store listing reads metadata and frozen node IDs in one read transaction, capped
at 100 windows per page. Its exclusive ID cursor is stable but not chronological;
the eventual UI must not imply creation-time ordering. Pagination tests verify no
duplicates/missing rows, empty arrays, input limits and retained canceled-window
membership after node deletion.

The authenticated, private/no-store management endpoint
`GET /api/v1/admin/maintenance-windows` returns `windows` and `next_cursor`.
`limit` defaults to 25, must be a single integer from 1 through 100 when supplied,
and rejects empty or repeated values. `after` is the exclusive ID cursor, bounded
to 128 bytes. An empty result is an array, and an empty cursor marks the last page.
This is ID ordering, not creation-time ordering; separate requests do not share a
database snapshot. The handler has a five-second query deadline, returns 400 for
invalid queries and a generic 500 for storage failures. API tests cover session
protection, no-store, empty results, membership, pagination and invalid inputs.
Listings also return persisted `selection` and `canceled_by` when available.
Legacy internal windows omit provenance rather than inventing their selection.

The internal member preview resolver now accepts explicit nodes or exact,
case-sensitive tags with an explicit all/any mode. It rejects ambiguous mixed
selectors, duplicates, empty matches and missing explicit members, and returns
ID-sorted IDs and display names. At most 100 selectors and 100 matched members
are accepted; it rejects excess matches instead of silently truncating the scope.
Tests cover selection semantics, invalid inputs, changed tag membership, the match
limit and absence of window writes. Creation must reuse this resolver within its
write transaction and compare confirmed membership; the current read-only preview
alone does not guarantee that a subsequent create uses the same members. Confirmed
creation uses the same resolver inside its writer transaction.

An internal full-window preview now combines reason, display timezone, normalized
UTC millisecond boundaries, canonical selection and ID-sorted named members. It
shares field validation with internal creation. A versioned SHA-256 confirmation
digest includes both draft and member IDs/names; reordered selectors and normalized
round trips are equivalent, while renamed members or changed membership require
new confirmation. Caller-owned slices are not mutated. Tests cover normalization,
invalid times/reasons/zones, round trips, membership/name changes and no writes.
This digest is not an authorization token. Transactional creation must recompute
it against current data and preserve selection/audit/idempotency state atomically,
as implemented by confirmed creation.

Internal confirmed creation now re-resolves the draft under the maintenance
writer transaction and rejects a changed digest. Migration 025 stores selection
provenance and create-request replay identities. The window, frozen members,
selector, replay record and existing audit-log entry commit atomically. Actor IDs
must identify authenticated users; the audit foreign key rejects unknown actors.
The request digest binds typed draft content, preview digest and actor. Successful
same-content retries return the original window ID before rechecking mutable nodes;
different content under the same request ID conflicts. Replay records are retained
with the bounded window history, with no automatic expiry or window deletion API.
Tests inject audit failure to prove rollback, check stale confirmation and replay
after node deletion. Eight concurrent create requests produce one window; eight
same-actor cancel requests produce one cancellation. Close/reopen and full staged
database restore preserve provenance, cancellation, audit and original request
results. This validates current-schema restoration, not all released-version
upgrade combinations or external notification behavior after restoration.

## Management mutations

`POST /api/v1/admin/maintenance-windows` accepts `draft` (use the normalized preview
draft), `preview_digest`, and a caller-generated `request_id` of 1–128 ASCII
alphanumeric/underscore/hyphen characters. The request limit is 64 KiB. First
creation returns 201 with `window_id` and `replayed:false`; identical retries return
200 and the original ID with `replayed:true`. Keep the same typed body and request
ID after a lost response. The preview digest uses canonical selection ordering,
but request replay identity binds the exact typed body, confirmation and actor;
changing selector order under an already-used key is a conflict.

New creation requires an end strictly after the current server time. An already
started window is permitted for ongoing work; an entirely ended window is rejected.
Successful historical retries remain valid after expiry/cancellation or node
deletion. Preview itself still permits historical dates. UTC year bounds are
validated after offset normalization. There is no editing or destructive deletion.

`POST /api/v1/admin/maintenance-windows/:windowID/cancel` accepts only `revision`,
with a 4 KiB body limit. It returns `window_id`, incremented `revision`,
`canceled_at` in UTC milliseconds, and `replayed`. Retrying the original confirmed
revision by the same authenticated actor returns the original result. Another
actor or revision conflicts. Ended uncanceled windows cannot be canceled. Migration
026 retains the confirmed revision and actor. Cancellation, action provenance and
audit commit together; failed audit inserts leave the window unchanged.

Both endpoints require session/CSRF, return private/no-store, reject unknown fields
and trailing JSON, and have five-second operation deadlines. Actor identity comes
only from the session. Errors: 400 `invalid_maintenance`; 404
`maintenance_not_found`; 409 `maintenance_preview_changed`,
`maintenance_request_conflict`, `maintenance_conflict`, `maintenance_expired`, or
`maintenance_limit`; generic 500 `maintenance_unavailable`. A removed/empty selection
may return 400; obtain a new preview. Mutation API tests cover authentication, CSRF,
strict input, confirmation conflicts, replay, provenance and retained deleted members.

Database restoration rewinds requests and cancellations to the snapshot. Requests
already in the snapshot replay; operations after the snapshot may execute anew on
retry. Review restored windows/audit before retrying uncertain browser requests.

## Management UI and live acceptance

The notification center's Maintenance tab provides a one-time window form,
explicit-node multiple selection, all/any tags, full preview with named members,
confirmation, status/history listing, pagination and revisioned cancel confirmation.
Input dates use the browser's IANA timezone and preview returns normalized UTC
instants. Lists show upcoming/active/ended/canceled status, frozen members,
provenance, actors and cancellation time. Refresh also reloads current node options;
historical membership does not change when the inventory is refreshed.

Form changes invalidate previews. Before either mutation the tab persists the
original request to sessionStorage. Lost responses retain and lock that operation
for identical retry, including after reload. Invalid recovery records retain
uncertainty and block new mutations; explicit clearing requires result review.
Unavailable browser storage prevents sending. Definite validation/conflict
rejections permit new preview; request-identity conflict retains the record. A late
response may clear only its own record, never a newer operation's record. Closing
the tab can lose recovery history, and snapshot restore can rewind server identities.

The delivery view explains the persisted `maintenance` cancellation reason and
links to the maintenance tab. Incident details show current scope and recorded
decisions; individual canceled jobs can expand their exact recorded window links.
An empty history does not prove there was no suppression before tracking existed.

### Recorded context (migration 027)

`incident_maintenance_decisions` records one row per incident/window/decision kind,
maintaining first/last decision timestamps. `observation` means known-event
evaluation under active maintenance, not proof that a message was due;
`observation_gap` means the interval since the last known observation overlapped
maintenance, not proof of an outage or recovery inside that interval; `delivery`
means the claim/pre-send path suppressed an old/current queued job. The separate
`delivery_maintenance_links` table records exact job/window association while that
job is about to be canceled. Later post-window jobs do not inherit those links.
Recording, cancellation, repeat-clock reset and incident changes share the same
writer transaction; failed link/decision inserts roll back the entire operation.
Decision/link writes use batches of at most 200 window IDs (204 bound parameters),
including all batches in that transaction. Tests fail a later batch to verify
earlier inserts and job/incident changes do not partially commit.
There is no inferred historical backfill. Legacy windows/decisions remain unknown
when no new processing decision has been recorded.

Management read APIs:

- `GET /api/v1/admin/incidents/:incidentID/maintenance`: `windows` (window identity,
  reason, timezone, boundaries, optional cancellation, page-query `active` flag and
  typed `decisions`), `next_cursor`, `checked_at`, `node_exists`, `node_in_maintenance`.
  Current scope uses frozen membership, independently of past decision history.
  Deleted nodes retain history but are not claimed to have active current scope.
- `GET /api/v1/admin/notification-deliveries/:deliveryID/maintenance`: only recorded
  job/window links, with `recorded_at`, `windows` and `next_cursor`. Never derives
  links from overlapping timestamps alone.

Both are session-protected, private/no-store (including unauthorized/errors), have
five-second deadlines and read each page in one database snapshot. `limit` defaults
to 25, accepts one integer 1–100; `after` is an exclusive ID cursor up to 128 bytes.
Repeated/invalid pagination is 400, missing objects 404, storage errors a generic
500. Empty collections are arrays. Separate pages can reflect different times;
current scope is a query snapshot, so the UI labels it and offers refresh rather
than treating it as a continuously updated assertion.

The incident context loads independently of delivery history, so its read failure
does not hide deliveries. Job-specific context loads only when expanded; stale
responses cannot replace a newer selected subject. Window boundary/cancellation
times use the window timezone; decision/check times use the browser timezone with
an explicit label. These are processing times, not actual fault/recovery/receipt
times. Store tests cover overlap, stable pagination, no invented history, deleted
members, backup/restore, gap/worker sources, later-job isolation and insert-failure
rollback at evaluation/claim/pre-send. API tests cover access/cache and query bounds.
Real browser screenshots cover active and ended context and exact queued-job links
in both themes at 360/768/1440 px.

Decision records are bounded per incident/window/kind; delivery links grow with
canceled jobs. Existing incident/outbox retention does not automatically purge them.
Restoring pre-027 backups creates empty tracking tables during migration, without
backfilling causes. Restoring a current-schema backup preserves recorded links.

`TestMaintenanceAPIRuntimeNotifications` combines authenticated preview/create/cancel,
the actual evaluator and running workers with an isolated HTTP receiver. It checks
in-window recovery sends nothing, an older queued firing is canceled, a fresh
continuing fault sends one summary, and restarted evaluation/worker sends no duplicate.

The opt-in `TestLiveMaintenanceBrowserLifecycle` uses the embedded frontend and
real SQLite/API/queue worker. It verifies tags and explicit selections, preview
invalidation, membership changes, lost create/cancel responses across reload,
corrupt recovery records, blocked storage, refreshed inventory and the suppressed
delivery explanation/link. Screenshots cover preview/history in both themes at
360/768/1440 px, with no page overflow and no browser exceptions. Screenshots were
visually inspected. Fixtures contain no real infrastructure or credentials.
It runs both a queued-but-unsent case (zero receipts/attempts) and a real request
already accepted by the local receiver. The latter delays the 204 response until
maintenance cancels the job, then verifies one receipt, a delivered attempt and
the UI explanation that cancellation cannot withdraw an already sent notification.

`TestMaintenanceRaceRetainsCompletedAttempt` uses a real evaluator, worker and
HTTP receiver. Cases cover success, permanent rejection, lost response, a policy
disabled during the send and ACK before any maintenance evaluation. Canceled jobs
keep their terminal state; restart after window cancellation produces one fresh
summary only for a continuing enabled fault, with a new logical idempotency key.
Window existence alone does not invent a past suppression link. Store tests also
cover identical replay, conflicting/superseded tokens, a newer summary completing
before the old result, and atomic rollback if legacy-history insertion fails.

Run after building the embedded assets:

```powershell
$env:MYPROBE_TEST_MAINTENANCE_BROWSER='1'
$env:PLAYWRIGHT_MODULE='<absolute path to installed playwright module>'
go test ./internal/httpapi -run '^TestLiveMaintenanceBrowserLifecycle$' -count=1 -v
Remove-Item Env:MYPROBE_TEST_MAINTENANCE_BROWSER
```

Playwright and Edge are required locally; standard tests skip this browser test
without opt-in. CI still runs the runtime, send-race and Store/API tests. Full
released-version integration across branches and the proposed capacity remain
separate release evidence; successful screenshots alone do not establish them.

Full-window preview is exposed through
`POST /api/v1/admin/maintenance-windows/preview`, with the same session/CSRF,
private/no-store, 64 KiB strict-body and five-second deadline protections as member
preview. Its body is a draft, for example:

```json
{"selection":{"kind":"tags","tags":["prod"],"tag_mode":"all"},"reason":"planned work","timezone":"Asia/Shanghai","starts_at":"2026-10-11T10:00:00+08:00","ends_at":"2026-10-11T11:00:00+08:00"}
```

The response contains `draft`, `members`, and `preview_digest`. Use the returned
normalized draft for confirmation. Invalid drafts return 400 with
`invalid_maintenance_draft`; excess matches use `maintenance_member_limit`.
Tests verify permission checks, UTC millisecond normalization, digest round trips,
invalid fields/times, strict decoding, request limits and absence of writes.
Preview does not enforce future-only dates or reserve storage capacity; creation
enforces its own admission checks without claiming preview has
already created, scheduled or frozen a window.

Scope-only preview is available at
`POST /api/v1/admin/maintenance-windows/preview-members`. It requires a management
session and CSRF token and returns private/no-store responses. The body is limited
to 64 KiB, rejects unknown fields and trailing JSON, and contains `selection`:

```json
{"selection":{"kind":"tags","tags":["prod"],"tag_mode":"all"}}
```

For explicit selection use `{"kind":"nodes","node_ids":["node-id"]}`.
The response contains the input `selection`, ID-sorted `members` (`id`, `name`),
and `member_count`. Empty/invalid selections return 400 with
`invalid_maintenance_selection`; more than 100 matched nodes return 400 with
`maintenance_member_limit`. Storage errors return a generic 500. Queries have a
five-second deadline. Tests cover login/CSRF, no-store, exact membership, strict
decoding, body limit and no window creation.

This endpoint previews scope only, not times, reason or a complete window. It
issues no creation approval token and reserves no membership. Full creation
confirmation must bind the window fields and resolved members and recheck them
transactionally; clients must not treat this result as an already frozen window.

The incident regression matrix now verifies two overlapping windows: canceling
the first does not resume notifications while the second is active, and either
natural expiry or cancellation of the final window allows exactly one fresh
summary before normal repeats. A separate already-notified incident test confirms
recovery during maintenance is recorded without a recovery delivery or catch-up,
while retaining the original delivered firing history.

## User behavior

First delivery supports one-time windows for explicitly selected nodes or a tag
selection resolved to a frozen node list when created. Show both the selection
description and frozen members. A new matching node does not silently join an
existing window. An empty match must be rejected rather than interpreted as all.

Store UTC start/end instants, the display timezone, reason and creator. Windows
are half-open `[start, end)`: suppress at start, permit evaluation at end. Require
end after start, bounded reason and membership, and optimistic revisions for
changes/cancellation. Retain canceled windows and historical memberships so a
restart can explain past suppression. Deleting a node does not erase that history.

Maintenance affects notification intent, not metric collection or incident state.
Pending/firing/resolved transitions and evidence continue normally. Maintenance is
separate from acknowledgement and silence; none of these means technical recovery.

## Notification integration

Integrate inside the incident transaction before creating a delivery, inside claim
selection, and inside the last pre-send lease check. All three must use the same
window interpretation. Network I/O remains outside transactions; a request already
sent cannot be recalled. Do not advertise an absolute no-send guarantee across
the final check/network boundary.

Suppressed transitions must not accumulate a queue of old messages. Record a
bounded per-incident suppression marker with the relevant event/observation time.
If a firing job already exists when maintenance starts, cancel or suppress it with
an explicit maintenance reason. If it was already attempted, preserve its known
result, or ambiguity if confirmation is unavailable, rather than claiming it was
never sent. A late provider result updates attempt history only.

After all overlapping windows end, reevaluate current evidence. A still-firing
incident with fresh evidence generates one current firing notification, then
returns to normal repeat rules. If evidence is stale, wait for observation;
maintenance ending must not convert stale metrics into a current fault assertion.
An incident resolved during maintenance receives no catch-up firing or recovery
notification, including when its initial firing was sent before the window.

Restart after an entire window must preserve these outcomes. Query historical
window overlap with the transition/last-observation interval rather than checking
only whether a window is active now. Define the conservative treatment of recovery
first observed after a data gap: do not invent its actual time inside maintenance.
The suppression marker and incident transitions commit atomically, preventing
duplicate end-of-window summaries after restart or concurrent workers.

## Work packages and gates

1. Add a new forward migration for windows and frozen membership. Implement
   validated storage, revision checks, bounded listing and overlap queries. Include
   backup/restore and time-boundary tests. Do not expose a working toggle yet.
2. Connect event enqueue, claim and pre-send checks with persisted suppression
   state. Cover already-pending/inflight jobs, overlapping windows and restart
   during/after maintenance. Preserve existing policy-current guards.
3. Add authenticated/CSRF-protected API with strict bounded input and audit records.
   Preview resolves tags and returns the exact frozen membership to confirm;
   execution must detect changed members/configuration instead of silently widening
   scope. Cancellation is revisioned and auditable, not a destructive delete.
4. Add management calendar/list and create/preview/cancel flow; display timezone,
   frozen members, active/upcoming/ended/canceled status and notification impact.
   Incident and delivery views must explain maintenance suppression separately.
5. Run real API/browser/local-channel tests at three widths in both themes. Verify
   zero notifications for a fault wholly inside maintenance; one current firing
   notification for a continuing fresh fault; no duplicate after restart.

Required boundary matrix: before/start/end/after; explicit nodes and frozen tags;
empty match; overlapping windows; cancel while active; node deletion; stale data;
firing/recovery in-window; first recovery after an observation gap; pending job;
leased-but-unsent job; network already started; application downtime spanning the
window; policy edits during maintenance; concurrent create/cancel; full DB restore.

Maintenance does not yet alter HTTP availability denominators. That is a separate
service-monitor integration contract: retain raw observations, report excluded
maintenance duration explicitly, and do not label existing success rate as SLA.

## Release boundaries

Proposed initial bounds: 1000 windows and 100 nodes per window, one-time only.
Validate these against workload evidence before claiming supported capacity.
The on-disk Store benchmark exercises 100 nodes under 1/100/1000 overlapping
windows and verifies exact populations, quota rejection and suppression. Batched
decision/link writes reduced the measured 1000-window warm pass median from about
5.08 s to 1.47 s on one Windows machine. This is not a full-system capacity claim;
method, samples and outstanding workloads are recorded in
[MAINTENANCE_CAPACITY.zh-CN.md](MAINTENANCE_CAPACITY.zh-CN.md).
Recurring calendars, notification aggregation and service-wide quorum rules are
outside this first window implementation. Acknowledgement and incident silence
remain separate ALT-03 packages; completion of windows alone does not finish ALT-03.

No migration number is reserved by this document. Reconcile numbering against the
dependency branch and all release-bound changes before merging. Do not modify a
released migration, merge PRs or deploy as part of this implementation contract.
