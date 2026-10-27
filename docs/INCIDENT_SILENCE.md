# Incident silence and suppression combination contract

Roadmap SIL-01/SIL-02, implemented and unreleased on `feature/incident-silence`,
draft PR #57 stacked on acknowledgement PR #56. This adds incident-scoped notification silence
to the existing state machine/outbox. It does not replace maintenance or condition
state. No merge/deployment is included. Notification aggregation remains separate.

## Behavior

- Only a firing incident accepts a new silence. Server time starts the half-open
  interval `[start,end)`, with a requested duration of 60 seconds through 7 days.
  The API returns exact UTC millisecond boundaries; it does not trust client time.
- At most one effective silence interval overlaps another for the same incident.
  Setting a second active interval is a conflict; cancel the current one or wait
  for expiry before creating another. History is retained, rather than replaced.
- Silence suppresses notifications, including recovery observed during silence.
  Collection/evaluation/trigger/recovery evidence continues. Recovery closes the
  event and its active silence; a later episode is not covered by the old interval.
  Rule disable/change/deletion closes active silence with that administrative
  reason, without manufacturing a technical recovery notification.
- Creation atomically cancels pending/in-flight intent and resets the repeat clock.
  Canceled jobs never resume at expiry/cancel. After the final effective suppression
  ends, a continuing fault needs a fresh known observation to enqueue one current
  summary, then resumes normal repeats. Unknown evidence does not force a send.
- Creation/cancellation advance the incident mutation watermark (`updated_at`),
  preserving `observed_at` and continuity timestamps. An evaluation captured before
  the action cannot commit a stale transition afterwards. Backdated mutations are
  rejected; action timestamps are not fabricated metric evidence.
- A send already past the final check cannot be recalled. The latest canceled
  attempt can record its real late result without reviving retry/clock authority.

## Maintenance and silence

Any effective maintenance window OR event silence prevents notification intent.
Removing either one leaves the other intact. They retain separate records and
actual processing decisions. A cancellation that matches both stores
`maintenance_and_silence`, with exact links to both; one cause stores `maintenance`
or `silence`. No historical cause is inferred just from timestamps.

Creation examines maintenance only when actual jobs remain to cancel. That queue
decision uses maintenance kind `delivery`, which now means queue suppression
from claim/pre-send or a silence action, rather than only a worker phase. Creating
silence with no jobs does not invent a maintenance queue decision. A canceled job
keeps the causes recorded by its original cancellation; later scopes do not
retroactively change it. New post-window jobs do not inherit those links.

Fresh continuing observations and worker checks also inspect past overlap across
an observation gap or job lifetime, preventing old intent from escaping after
downtime spanning a complete interval. Cancellation/recovery clip historical
silence duration. Recovery first observed after a gap is dated when observed;
the system does not assume it happened inside silence.

## Combined acknowledgement

`acknowledge:true` creates the first acknowledgement and silence in one transaction.
The silence reason becomes the note only if an acknowledgement is newly created.
An existing first actor/time/note is preserved; the result states whether a new
acknowledgement was created. An internal request namespace cannot collide with
portable standalone ACK IDs. Either both state changes/audits commit or neither
does. Standalone acknowledgement still does not silence.

## Persistence and mutation replay

Forward migration 029 adds `incident_silences`, `incident_silence_requests`, typed
`incident_silence_decisions` and exact `delivery_silence_links`. Actor names are
frozen. All affected state, queue cancellation, links, optional ACK, audit and
request result share the SQLite writer transaction. Decision inserts are batched
in groups of at most 200 IDs; any later failure rolls the whole operation back.

Create/cancel request IDs bind operation, target, actor and normalized typed
content. Original result JSON freezes boundaries/cancel time/ACK-created status;
successful retries replay even after expiry, cancellation or recovery. Changed
content/actor under the same ID conflicts. Cancellation requires the current
revision and a still-active silence, retaining actor/time/history. Reusing a
rejected request does not reserve it as a successful operation.

Optional reason uses the ACK note normalization and 1000 UTF-8 byte bound. Audit
includes duration and ACK-created status, not note text. Public nodes, Agent data
and notifications receive no silence reason/actor fields. History and retry
identities are incident-owned and persist in full backups. Restore rewinds newer
actions; pre-029 backups create empty tables with no inferred history. There is
currently no automatic silence/incident purge; monitor database growth.

## Protected API and management UI

All routes require an administrator session and CSRF for mutations. Private
`Cache-Control: no-store` is applied before authentication. Mutations accept
strict UTF-8 JSON, reject unknown fields/trailing content and cap bodies at 16 KiB.
Actor/time come from the server; storage failures return a generic error. Reads
and writes have a 5-second context deadline.

| Route | Request/result |
| --- | --- |
| `GET /api/v1/admin/incidents/:incidentID/silences` | State, checked-at time, current active silence and bounded history/decisions |
| `POST /api/v1/admin/incidents/:incidentID/silences` | `request_id`, `duration_seconds`, `reason`, `acknowledge`; 201 new / 200 original replay |
| `POST /api/v1/admin/incidents/:incidentID/silences/:silenceID/cancel` | `request_id`, current `revision`; 200 including original replay |
| `GET /api/v1/admin/notification-deliveries/:deliveryID/silences` | Only exact recorded delivery links, with recorded-at time |

Reads use one SQLite snapshot per page. `limit` is 1–100 (default 25); `after`
is the last ID from the previous page, returned as `next_cursor`. IDs are stable
pagination order, not chronological order. The active interval is returned
independently of the history page. Invalid queries/requests return 400, missing
objects 404, active interval/revision/state/request-identity conflicts 409.

Incident details expose exact deadline, snapshot check time, browser timezone,
actor/reason/history, cancellation and separate standalone/combined actions.
Reasons render as escaped multiline text. Combined success refreshes the existing
ACK view; an earlier ACK remains unchanged. Delivery details explain maintenance,
silence or both and lazily fetch their independent recorded contexts. Activity is
as of the displayed check time; refresh after expiry to see current state.

Before posting, the UI writes the original typed operation into
`myprobe-incident-silence-pending-v1` in tab session storage. Unknown response,
401/403/server errors and identity conflicts retain it. Reload or selection of a
different incident never changes its target, content, revision or request ID.
Known 400/404/409 rejection (except identity conflict) ends that operation and
refreshes state. A late response clears only its own stored record. Corrupt or
blocked storage prevents a new operation; manually clearing after review does not
undo a committed server action. Closing the tab can lose recovery context.

## Acceptance and remaining release work

Store tests cover queued cancellation, mutation watermark, fresh-only summaries,
expiry/unknown evidence, revisioned cancel/replay, recovery/no catch-up, new episode
isolation, existing ACK preservation, combined rollback and each order of removing
maintenance/silence. Concurrency, restart/full restore, held leases, late actual
results, cancellation rollback, actor identity conflict and administrative closure
are covered. Context tests verify pagination/current interval, actual links and no
links inherited by later jobs. API tests cover session/CSRF/no-store, input bounds,
server actor/time, conflicts, post-recovery replay, private data isolation and
generic storage failures.

Real evaluator/worker/local HTTP receiver tests cover continuing-fault expiry,
recovery inside silence without catch-up, independent later episode and actual
late ACK while a send is canceled. Browser tests cover lost create and cancel
responses with cross-reload/cross-incident replay, three malformed recovery
records, blocked storage, active/request identity conflicts, read retry, escaped
notes, combined ACK, dual causal links and independent maintenance cancellation.
The local receiver gets one fresh fault summary and its later recovery; the old
job stays canceled. Both themes at 360/768/1440 px pass without horizontal overflow
and screenshots were inspected. ACK and maintenance browser regressions are also
required for this stack.

Opt in with `MYPROBE_TEST_SILENCE_BROWSER=1` and a resolvable `PLAYWRIGHT_MODULE`,
then run `go test ./internal/httpapi -run '^TestLiveIncidentSilenceBrowser$' -count=1 -v`.
The fixture uses Edge headless, an isolated database and a loopback receiver.

Local Store/API/alerts tests, frontend installation/build, vet, command build and
diff checks pass. The whole-suite known Windows collector path failure remains
isolated in PR #47; npm reports the existing five advisories and chart chunk size
warning. No dependency changes are included. Release integration, migration
reconciliation across the unmerged stack, retention/capacity gates and owner
approval remain pending. Aggregation and M3–M5 retain their original roadmap scope.
