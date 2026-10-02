# Incident state and durable notification delivery (M2 / ALT-01, ALT-04)

Status: in development. Do not release or mark implemented until the new engine,
legacy migration, worker, API/UI and failure tests are integrated.

## Problem and scope

The existing engine combines condition state and notification attempts in
`alert_states`. It skips evaluation when a channel is disabled or encryption is
unavailable, and evaluates then sends before persisting the result. A crash in that
window cannot be distinguished from an unsent notification. This change establishes
one persisted incident state machine and a separate transactional outbox. Scoped
policies, maintenance and aggregation remain the next M2 tasks.

## State contract

- `pending -> firing -> resolved` describes the observed fault, independently of
  channel delivery. A partial unique index permits one active incident per rule/node
  fingerprint. Existing single-node rules are the initial policy adapter.
- First-failure, threshold-crossing, recovery and last-known-observation times are
  distinct. Unknown/stale observations cannot resolve a fault and interrupt pending
  trigger/recovery windows. Resource sample age is checked against actual collection
  and reporting periods; latency observations must have usable current samples.
- A separate recovery window defaults to the trigger duration for new rules;
  migration explicitly preserves legacy immediate recovery. Disabled, deleted or
  materially changed rules end incidents with a management reason, not a technical
  recovery message. Historical snapshots survive rule/node deletion.
- `alert_states` is retained as legacy data, not a second evaluation authority.
  Existing notification history remains readable. Historical incidents that cannot
  be reconstructed are not invented.

## Durable delivery

Incident transitions and queued notifications commit in one SQLite writer
transaction. Each logical notification has a unique key comprising incident,
transition/reminder sequence and channel. Worker leases and attempt records persist
across restarts. Network calls take place outside transactions with bounded timeout.

At most five attempts use 30s/2m/10m/30m backoff with jitter; HTTP Retry-After is
honored, and permanent authentication errors fail without retry. Lease expiry can
mean a remote receiver already accepted the message: delivery is at least once,
not exactly once. Only a matching unexpired lease can complete an attempt. A stale
worker cannot overwrite a newer attempt. Provider errors are stored as bounded,
safe classifications without credentials, full URLs or response bodies.

Before sending, recheck incident, rule, channel and cancellation state. Cancel old
queued firing messages once the incident resolves. Recovery messages retain incident
context so an uncertain earlier delivery is not presented as an unrelated event.
Condition evaluation continues when delivery is unavailable. Existing single-channel
rules retain their channel and repeat interval; future policy fan-out uses one job
per channel. Slow delivery must not block the evaluation loop.

## Upgrade and compatibility

Use new forward-only migration 015; 014 is introduced by the independently pending
batch-management PR. The migrations are independent and their final numbering must
be checked before release. Current active/pending legacy states become marked
imported incidents. Available timestamps are marked approximate, not claimed to be
original start times. Successful legacy notifications seed reminder scheduling and
are not re-enqueued on upgrade. Failed legacy attempts retain a bounded retry path.
Migration and reopen must be idempotent.

Rule/API compatibility and configuration transfer must preserve recovery settings.
The new incident and delivery APIs are administrator-only and paginated. A fault
list and delivery history must clearly distinguish fault state, observation freshness
and send outcome. Public node payloads and the Agent protocol remain unchanged.

Back up before upgrading. A downgrade requires a compatible binary or restoration
of the pre-upgrade database; continuing new outbox state with an old engine is not
supported. Do not edit released migrations.

## Required acceptance evidence

- Same condition concurrently evaluated creates one active incident and one job.
- Failure/recovery windows, missing data, thresholds, repeats and rule lifecycle
  have deterministic behavior; transient unknown data does not become recovery.
- Event update plus queue insertion rollback together if any write fails.
- Crash/reopen at pending, queued, leased and finishing points preserves state;
  expired leases recover, stale completions fail and retry budgets stay bounded.
- Successful legacy notifications do not all resend after migration; failed and
  pending legacy states preserve their intended continuation.
- Disabled/broken channels do not suppress incident tracking. Permanent errors,
  transient errors, Retry-After and ambiguous timeout are visible and tested.
- APIs enforce login, CSRF for mutations, bounded filters/cursors and privacy;
  light/dark 360/768/1440px browser workflows distinguish events from deliveries.
- Update changelog, product spec, README, generated assets and complete repository
  checks before making the PR ready. Capacity and 24-hour evidence remain M5.

## Current implementation evidence

Migration 015 and upgrade tests are implemented. They cover active/pending and
failed-recovery legacy states, disabled rules, retained notification history,
explicit legacy recovery defaults, non-duplication on migration/reopen, one-active
incident constraints, retry/lease constraints and snapshot survival after source
node deletion. Successful old sends create no new queued jobs. Failed attempts
start with attempt count one and their original cooldown (minimum 30 seconds);
raw old provider errors are not copied into new payloads.

Store, alert, HTTP API and backup package tests pass with the new schema. The
service now uses the incident queue: this branch is still not ready to deploy.
The transactional observation/outbox repositories and a single-job worker are
implemented and covered by package tests. Coverage includes concurrent creation
and claiming, transaction rollback, stale observations, recovery windows,
configuration changes, expired leases, stale completions, retry budgets and
sanitized compatibility history. HTTP failures distinguish permanent client
errors from timeouts, throttling and server failures; Retry-After supports delta
seconds and HTTP dates with overflow protection. Backoff uses positive jitter.
The worker performs network I/O outside database transactions and rechecks its
lease and observation freshness immediately before sending.

The scheduler now records observations independently of channel decryption and
network delivery. Four workers consume leased jobs with bounded send contexts.
Lifecycle regression tests explicitly run evaluation and delivery separately;
a missing-key test proves evaluation still creates a firing incident and job.
Recovery duration is validated independently and defaults to trigger duration.
Resource and latency observations expire after max(60 seconds, three configured
intervals); timestamps more than 30 seconds in the future are unknown. Empty
disk/network samples are unknown. A fresh failing latency target can establish
a fault, but recovery requires every assigned target to have fresh complete
results. Boundary and resource evaluation tests cover missing/stale samples.
Next work is administrator APIs/UI, and additional
end-to-end/browser validation.
A freshness change after claiming defers the job but consumes that reserved
attempt, retaining an explicit canceled attempt for diagnosis.

## Administrator read API

- `GET /api/v1/admin/incidents`: optional `state` (pending/firing/resolved),
  `node_id`, `before` (exclusive sequence cursor) and `limit` (1–100, default 50).
  Returns `incidents` and `next_before` (zero when the page is shorter than limit).
- `GET /api/v1/admin/incidents/:incidentID/deliveries`: same pagination; returns
  `deliveries` and `next_before`. A missing incident returns 404.

Both require administrator authentication. Results are newest sequence first;
filters apply to current incident state, so these are live queries, not snapshots.
A full last page may return a cursor followed by an empty page. Payloads, rule
snapshots, lease tokens and idempotency keys are omitted from these responses.
API tests cover authentication, invalid pagination, missing incidents and private
field exclusion. Store tests cover cursor ordering and combined filters.

## Notification center integration (browser acceptance pending)

The notification center has a separate incident tab with state filtering, cursor
pagination, stale-data and approximate-import notices, and per-incident delivery
history. Loading and retry errors remain visible; overlapping detail requests
cannot replace the latest selected incident. Existing delivery history remains.
The rule editor preserves explicit recovery duration, including zero; rule cards
display that duration. TypeScript and production build pass. Responsive/theme,
keyboard, real-server lifecycle and failure browser acceptance are still pending.

Webhook deliveries include `incident_id` and an `Idempotency-Key` header stable
across attempts of the same job. Receivers must implement deduplication to use
that header; this does not guarantee exactly-once delivery. Notification overview
counts explicitly describe recent delivery history, not current fault counts or
proof that every channel is healthy.

Local full Go tests pass except the existing Windows collector absolute-path
fixture (tracked separately in PR #47). `go vet ./...` and `go build ./cmd/...`
pass. Browser acceptance remains pending; these checks do not establish it.

## Local browser evidence (2026-10-02)

A separate loopback server and temporary SQLite database were exercised through
headless Microsoft Edge/Playwright using actual authentication and APIs. Twenty-six
synthetic offline nodes produced incidents while their channel was disabled, with
no external notification sends. Checks passed for 25-item first page, loading all
26 events, resolved/firing filters, per-event empty delivery history and no browser
page errors. Screenshots were captured at 360/768/1440 px in light/dark mode; all
six combinations passed document-width overflow checks. The 360 px dark screenshot
was visually inspected. The first test failure was an exact accessible-label
matcher including native select option text; a role/name matcher passed.

This evidence does not yet cover keyboard focus, network-error UI, queued/retrying
and recovered events in the browser, or visual inspection of every screenshot.
Those remain required before this PR is ready.

A follow-up browser pass verified Enter activation moves focus to the delivery
heading, making details reachable after long lists. Injected HTTP 503 responses
for list and delivery queries displayed their errors; retrying against the real
API cleared the errors. Existing list records remained visible after a refresh
failure. Type checking and regenerated production assets pass after the focus fix.
Queued/retrying/recovered browser scenarios and remaining visual review are still
outstanding; this is not a complete PR acceptance claim.

Provider integration tests now use a real loopback HTTP receiver: a 429 with
Retry-After=120 remains queued before its deadline, then succeeds with the same
idempotency header; a 401 terminates the job without another attempt. Both retain
the incident's firing state, and private response diagnostics are absent from
compatibility history. These tests exercise sender, worker and database together.

## Per-attempt diagnostics

`GET /api/v1/admin/notification-deliveries/:deliveryID/attempts` requires an
administrator session and returns at most five attempts in ascending attempt
order. Each contains number, start/completion timestamps, outcome and safe error
classification. Missing deliveries return 404. Lease tokens and provider response
bodies are excluded. API regression tests cover anonymous rejection, missing jobs,
an active attempt and omitted private fields; provider integration tests cover
failed-then-delivered history. The UI supports loading, retry errors and empty
history, and explicitly explains that unknown outcomes may already have delivered.
This newly added detail expansion still needs browser acceptance.

Recovery-form browser verification found that existing notification center save
buttons inherited the design system's default button type and did not submit.
Channel/rule/template submit buttons now explicitly use type=submit. New rules
start with recovery linked to trigger duration; editing an explicit zero preserves
immediate recovery. A browser test intercepted rule writes and verified a 45-second
trigger submits a 45-second recovery by default, and an explicit override submits
zero. The initial no-request failure was reproduced before the button fix and the
same test passed afterward. No external notifications were sent.
