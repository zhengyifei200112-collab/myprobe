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
acceptance of a new post-window observation/job. This does not yet reset the repeat
clock for previously delivered incidents with no outstanding job when the server
misses the window. Downtime-spanning incident suppression markers are still missing; do not expose window
creation to users until those paths are integrated. Tag resolution, listing,
selection provenance, audit actors and full backup tests remain pending.

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
