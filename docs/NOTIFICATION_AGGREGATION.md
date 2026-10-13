# Notification aggregation (AGG-01, in development)

`feature/notification-aggregation` is stacked on silence PR #57. This work is
unreleased. Aggregation changes transport, not incident identity: every event
keeps its logical delivery, repeat clock, cancellation causes and attempt history.

## Queue contract

- A policy configuration can explicitly set `aggregation_seconds` to 5–300.
  Zero or omission retains immediate single delivery. Existing definitions and
  unbound legacy rules are not silently delayed. The new-policy UI starts
  at the roadmap's suggested 30 seconds and shows the added wait.
  On an existing policy update, omission preserves its current setting so an
  older editor cannot accidentally disable it; explicit zero disables it.
- Forward migration 030 creates transport aggregates, immutable attempt payloads
  and exact attempt/member links; logical deliveries point to their aggregate.
  Queue creation and assignment are in the observation writer transaction.
- Group by policy ID/revision, channel, provider, notification type and the
  captured link origin/render version. A first
  member starts a bounded half-open window; later arrivals do not extend it.
  Deadline storage rounds upward to prevent early sends. Each shard holds at most
  10 events; larger storms create additional bounded shards. Firing and recovery
  are separate. A sealed aggregate never accepts new members.
- A worker reserves a group and per-event attempts atomically. Its one-shot final
  check validates each member against current condition, policy selection,
  destination, maintenance, silence and observation freshness. Inapplicable
  members are canceled; stale members wait independently. Other ready groups and
  single deliveries can proceed. A pre-send recovery of only prepared aggregate
  work does not manufacture a standalone recovery message.
- Final preparation freezes the canonical logical payload, included members and
  transport key before I/O. The destination's encrypted configuration is read from
  that same snapshot and returned privately to the sender, never copied into send
  history or serialized send metadata. Provider envelopes/custom templates render
  with this snapshot. Summary text is at most 1600 UTF-8 bytes; structured JSON is at most
  16 KiB. Names/message excerpts are clipped at UTF-8 boundaries. Members retain
  event/node IDs, original observation time and an event path. Custom webhook
  expansion exceeding the JSON transport limit fails permanently without sending.
- A retry with unchanged content and destination configuration version keeps its
  key; changed membership or a re-encrypted destination configuration gets a new
  key. A provider may have received an ambiguous earlier attempt, so changed
  retries can still duplicate previously included members. No exactly-once claim.
  Provider results update all included leaf jobs/history in one transaction.
  Canceled leaf jobs can keep their real late result without regaining retry or
  repeat-clock authority. Non-included members never receive that transport result.
- Leases recover prepared attempts as known unsent and authorized attempts as
  unknown. Leaf retry bounds/backoff/Retry-After remain authoritative; a transport
  count can exceed five when deferred subsets become eligible at different times.
  A group is called `finished`, not `delivered`, because member outcomes can differ.
- Parent epochs fence completion under the same writer. A response to its still
  current token can finish before a replacement claims it, even past wall-clock
  expiry. After takeover, the old real result can refine exact authorized attempt
  history and legacy receipt evidence only; current jobs, leases and clocks stay
  unchanged. Identical committed results replay; conflicting known outcomes and
  wrong tokens are rejected. A restored backup can rewind local send evidence and
  replay already received content; only a provider honoring its key can deduplicate.
- Changing aggregation alone does not close/recreate a technical incident. New
  queue assignments use the new policy revision; existing bounded tasks retain
  their original window and causal history.

## Management and authenticated links

The policy editor starts new policies at 30 seconds, accepts integer zero or
5–300, and shows that this adds a wait. Editing an existing policy uses its stored
value, including zero for old definitions. Policy cards and import previews show
the transport setting. Changing it does not rewrite an existing group's window.

In **Alerts → Notification aggregates**, each row shows its bounded window and
current counts of pending/inflight/delivered/failed/canceled logical jobs. Detail
shows current incident state separately from each historical attempt's exact
content and member associations. `included=false` is a prepared association,
not proof the event entered the send. Prepared attempts have null content.
An `unknown` result means a channel may have received the message; retries can
duplicate it. `finished` does not imply every member succeeded, and channel
acceptance is not proof of a person reading the notification. Refresh rereads
current data; loading another page reads a new snapshot, not a historical
transaction held across requests.

Protected read endpoints (all require an administrator session and set no-store,
including unauthorized/error responses):

| Endpoint | Response / cursor |
| --- | --- |
| `GET /api/v1/admin/notification-aggregates?before=0&limit=25` | `aggregates[]`, `checked_at`, `next_before`; descending group sequence |
| `GET /api/v1/admin/notification-aggregates/:id?before=0&limit=25` | `aggregate`, current `totals`/`members[]`, exact `attempts[]`, `checked_at`, `next_before`; descending attempt number |
| `GET /api/v1/admin/incidents/:id` | `incident`; allows opening an older event outside the current list page |

Limits are 1–100 (default 25). Before is a nonnegative integer; zero starts at the
latest record. Pass the returned nonzero cursor to exclude its boundary on the
next page; zero next cursor means exhausted. Duplicate cursor/limit parameters,
overflow and invalid values return 400. IDs use the existing portable 1–128
character grammar. Missing records return 404; storage failures return a generic
500 without database details. Empty collections are arrays. Leases, idempotency
keys, encrypted channel credentials and raw storage payloads are not exposed.
Group member count remains at most 10; attempt pages are bounded independently.

Set optional `MYPROBE_NOTIFICATION_ORIGIN=https://probe.example.com` before
startup to make new aggregate/event links absolute. It accepts only an HTTP(S)
origin with host and optional valid port, at most 512 UTF-8 bytes; credentials,
paths except a root slash, queries, fragments and internal whitespace are
rejected. Errors never echo the input. Compose passes the setting from `.env`.
Host/Forwarded headers never determine notification URLs. With no setting,
links are relative `/admin?...` paths; some external clients cannot open those
directly. The operator must configure an externally reachable origin for them.

Forward migration **031** adds a captured origin and renderer version. Existing
030 groups default to renderer 1, preserving their exact old payload and retry
key; new assignments use renderer 2. Renderer 2 sends an additive `snapshot_id`
and `group_path`, with an absolute/relative `event_path` on each member. One
content version retains its 64-hex digest across identical retries; membership
or link content changes produce a different digest. The group URL identifies
content across attempts, not one attempt or proof of delivery. Configuration
changes never rewrite old groups. These fields are notification data, not public
node API or Agent protocol changes. Existing API/worker clients can ignore them.

`/admin?aggregate=<id>&snapshot=<digest>` opens that version's attempts after
login. `/admin?incident=<id>` opens event detail. No secrets or sessions travel
in the URL. Unauthenticated pages fetch no private incident/group context.
Password login retains the target. GitHub navigation saves only a validated
canonical local target in the current tab for one hour, restores it on return
and clears it after authentication. Malformed/expired records are discarded;
arbitrary return URLs are never accepted. Storage restrictions do not prevent
login, but can lose the GitHub return target; reopening the original link works.

The viewer searches at most four 25-attempt pages automatically for a linked
content version, then offers loading older history. If the version is absent it
explains that condition without substituting another payload. Invalid, missing
and temporarily unavailable records have separate errors/retry actions. Member
navigation builds local routes from event IDs rather than following stored URLs.
Event detail includes its condition/message, start/end and stale observation
state even when it is outside the current list page. A late initial link read
cannot replace a different event explicitly selected while loading. Names and
messages render as text, including markup-like names. Authentication,
forward-only upgrades and backup rollback requirements remain unchanged.

## Measured receiver baseline

2026-10-13, Windows/amd64, Intel i5-13490F, Go 1.26.5. On-disk temporary SQLite,
one global expiry policy, one real loopback HTTP receiver and sequential worker.
Three independent fixtures per node count and mode. Medians below include the
first fixture; they are not percentiles or sustained capacity evidence.

| Nodes | Immediate requests | 30 s aggregate requests | Immediate enqueue/drain ms | Aggregate enqueue/drain ms | Immediate/aggregate body bytes |
| --- | ---: | ---: | ---: | ---: | ---: |
| 5 | 5 | 1 | 4.007 / 7.994 | 4.265 / 5.000 | 1655 / 1970 |
| 50 | 50 | 5 | 31.985 / 75.527 | 35.000 / 31.999 | 16550 / 18205 |
| 100 | 100 | 10 | 58.302 / 160.004 | 64.618 / 71.106 | 33100 / 36410 |

Aggregation cuts request count by up to 90% in these fixtures and preserves
structured event references, which increase total body bytes. Enqueue costs rise;
this is a notification-volume tradeoff, not a universal speed/bandwidth win.
The 30-second deadline is exercised by advancing the evaluator/worker clock,
not by waiting 30 wall-clock seconds. Drain excludes that added wait. Loopback
results do not predict public-provider latency, four-worker throughput, RSS,
queue percentiles, 24-hour stability or full-system capacity. The pre-prototype
100-node immediate median drain was 148.203 ms; the above comparison runs both
modes through the same aggregate-capable worker.

Reproduce with `MYPROBE_MEASURE_NOTIFICATION_STORM=1`, set
`MYPROBE_STORM_AGGREGATION_SECONDS` to `0` or `30`, then:

```sh
go test ./internal/alerts -run '^TestNotificationStormMeasurement$' -count=1 -v
```

## Validation and remaining delivery work

Store tests cover bounds, deadline/sharding, one owner, atomic queue/claim/member
completion and history, unchanged retry keys, changed subset keys, per-event retry
caps independent of transport count, final recovery/silence/unknown filtering,
prepared vs authorized lease recovery, fair progress of another shard, restart,
full restore and exact historical refinement after takeover. Policy/channel/
provider/type/window/revision grouping boundaries and transport-only edits are
tested. Configuration bounds and older-editor preservation are verified.

The real loopback receiver/worker tests cover partial silence and recovery while
an actual aggregate request is held, recording late results independently for each
included leaf without clock resurrection. An actual failed request and retry keep
the same body/key and respect the stored deadline. Existing Store, alert worker and
API suites pass. Provider-specific envelope limits, broad administrative races and
management-facing explanations still require acceptance before delivery. Existing
policy, ACK, silence and both maintenance browser flows pass on the aggregate-capable
worker at both themes and three widths.

The full local suite still has only the known Windows collector path failure,
isolated in PR #47. Vet, command build and diff checks pass; final CI evidence is
recorded in the PR and external roadmap rather than treated as a release claim.

Management/context tests additionally cover mixed current outcomes, exact actual
versus prepared membership, unchanged content across unknown retries, bounded
pagination/empty arrays, credential/key redaction, origin grouping and rollback,
maximum-length links, and migration 031 preserving old renderer payload/key.
API tests check session protection before input validation, no-store, invalid/
duplicate/overflow queries, missing records and no public notification leakage.

The opt-in real browser fixture creates a policy at 30 seconds, rejects 4, edits
to 5, and queues two expiry events. It waits the real five-second window. A real
HTTP receiver accepts the first request but closes before replying; after the
real 30–36 second retry backoff, it acknowledges an identical body/key. The UI
opens the original content after password login, navigates to both events,
retains unknown and successful attempts, handles read retry and invalid/missing
targets/content versions, and escapes markup-like names. It checks detail and
policy forms in light/dark themes at 360/768/1440 px. It also guards a late
initial read against replacing another selected event, and refreshes current
event closure after policy disable without changing the old send content.
GitHub **return navigation**
is simulated locally with an authenticated fixture session; this is not live
GitHub provider authorization. The auth suite covers provider/state contracts.

```sh
MYPROBE_TEST_AGGREGATION_BROWSER=1 go test ./internal/httpapi -run '^TestLiveNotificationAggregationBrowser$' -count=1 -v
```

Set `PLAYWRIGHT_MODULE` to the installed Playwright module if it is outside the
Node search path; the script uses headless Edge. Screenshots are disposable local
test outputs, not committed evidence or production data.

Provider-specific envelope/parse-mode acceptance, broader administrative races,
non-aggregate/service notification link coverage, published-version integration,
retention/capacity and owner approval remain release gates. M3–M5 remain in the
original roadmap; this batch does not complete AGG-01/ALT-05 or that full scope.
