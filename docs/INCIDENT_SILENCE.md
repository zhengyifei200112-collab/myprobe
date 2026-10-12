# Incident silence and suppression combination contract

Roadmap SIL-01/SIL-02, implementation in progress on `feature/incident-silence`,
stacked on acknowledgement PR #56. This adds incident-scoped notification silence
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

## Acceptance and remaining work

Store tests cover queued cancellation, mutation watermark, fresh-only summaries,
expiry/unknown evidence, revisioned cancel/replay, recovery/no catch-up, new episode
isolation, existing ACK preservation, combined rollback and each order of removing
maintenance/silence. Concurrency, restart/full restore, held leases, late actual
results and administrative closure are covered. Management API, recorded-context,
actual receiver and browser tests remain required before delivery.

Protected create/cancel/list/context APIs and UI must expose exact deadline,
scope, author/reason/history and explain the independent suppression causes.
Support original-request recovery across reload, actual local receiver and real
browser acceptance in both themes at 360/768/1440 px. Keep the feature unreleased
until these checks and dependency integration/migration reconciliation are complete.
