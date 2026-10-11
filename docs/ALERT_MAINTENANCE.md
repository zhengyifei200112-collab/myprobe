# Alert maintenance implementation contract

Status: implementation in progress; event enqueue suppression exists internally,
but maintenance is not exposed as a usable feature. Roadmap task ALT-03. This branch depends on scoped-policy PR #54 and
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
inside maintenance. Further overlapping/cancellation and worker-race integration
acceptance remains necessary; do not expose window
creation to users until those paths are integrated. Tag resolution,
selection provenance, audit actors and full backup tests remain pending.

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
Creation and cancellation remain internal pending their preview/audit contract.

The internal member preview resolver now accepts explicit nodes or exact,
case-sensitive tags with an explicit all/any mode. It rejects ambiguous mixed
selectors, duplicates, empty matches and missing explicit members, and returns
ID-sorted IDs and display names. At most 100 selectors and 100 matched members
are accepted; it rejects excess matches instead of silently truncating the scope.
Tests cover selection semantics, invalid inputs, changed tag membership, the match
limit and absence of window writes. Creation must reuse this resolver within its
write transaction and compare confirmed membership; the current read-only preview
alone does not guarantee that a subsequent create uses the same members. Selection
provenance persistence and the HTTP preview/create contract remain pending.

An internal full-window preview now combines reason, display timezone, normalized
UTC millisecond boundaries, canonical selection and ID-sorted named members. It
shares field validation with internal creation. A versioned SHA-256 confirmation
digest includes both draft and member IDs/names; reordered selectors and normalized
round trips are equivalent, while renamed members or changed membership require
new confirmation. Caller-owned slices are not mutated. Tests cover normalization,
invalid times/reasons/zones, round trips, membership/name changes and no writes.
This digest is not an authorization token. Transactional creation must recompute
it against current data and preserve selection/audit/idempotency state atomically;
that transactional creation integration remains pending.

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
must define and enforce its own admission checks without claiming preview has
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
an explicit maintenance reason. If it was already attempted, preserve ambiguity
in attempt history rather than claiming it was never sent.

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
Recurring calendars, notification aggregation and service-wide quorum rules are
outside this first window implementation. Acknowledgement and incident silence
remain separate ALT-03 packages; completion of windows alone does not finish ALT-03.

No migration number is reserved by this document. Reconcile numbering against the
dependency branch and all release-bound changes before merging. Do not modify a
released migration, merge PRs or deploy as part of this implementation contract.
