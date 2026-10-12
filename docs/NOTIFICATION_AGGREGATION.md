# Notification aggregation (AGG-01, in development)

`feature/notification-aggregation` is stacked on silence PR #57. This work is
unreleased. Aggregation changes transport, not incident identity: every event
keeps its logical delivery, repeat clock, cancellation causes and attempt history.

## Queue contract

- A policy configuration can explicitly set `aggregation_seconds` to 5–300.
  Zero or omission retains immediate single delivery. Existing definitions and
  unbound legacy rules are not silently delayed. The planned new-policy UI starts
  at the roadmap's suggested 30 seconds and must show the added wait.
- Forward migration 030 creates transport aggregates, immutable attempt payloads
  and exact attempt/member links; logical deliveries point to their aggregate.
  Queue creation and assignment are in the observation writer transaction.
- Group by policy ID/revision, channel, provider and notification type. A first
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
- Final preparation freezes the exact body, included members and transport key
  before I/O. Summary text is at most 1600 UTF-8 bytes; structured JSON is at most
  16 KiB. Names/message excerpts are clipped at UTF-8 boundaries. Members retain
  event/node IDs, original observation time and an event path. Custom webhook
  expansion exceeding the JSON transport limit fails permanently without sending.
- A retry with unchanged content keeps its key; changed membership gets a new
  key. A provider may have received an ambiguous earlier attempt, so changed
  retries can still duplicate previously included members. No exactly-once claim.
  Provider results update all included leaf jobs/history in one transaction.
  Canceled leaf jobs can keep their real late result without regaining retry or
  repeat-clock authority. Non-included members never receive that transport result.
- Leases recover prepared attempts as known unsent and authorized attempts as
  unknown. Leaf retry bounds/backoff/Retry-After remain authoritative; a transport
  count can exceed five when deferred subsets become eligible at different times.
  A group is called `finished`, not `delivered`, because member outcomes can differ.
- Changing aggregation alone does not close/recreate a technical incident. New
  queue assignments use the new policy revision; existing bounded tasks retain
  their original window and causal history.

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

Store tests currently cover bounds, deadline/sharding, one owner, atomic member
completion/history, unchanged retry keys, changed subset keys, per-event retries,
final recovery/silence/unknown filtering, prepared vs authorized lease recovery and
fair progress of another shard. Existing Store, alert worker and API suites pass.
The actual loopback receiver measurement confirms grouping is connected to the
real worker. More restart/full restore, queue rollback, real in-flight mutation,
policy/channel/type separation and transport-boundary evidence is still required.

Management configuration/history/context APIs and UI, authenticated event deep
links, unknown-outcome explanations and both-theme/three-width browser acceptance
remain part of AGG-01/ALT-05. Event paths currently record the intended route;
they must not be advertised as completed clickable deep links yet. Published-version
integration, retention/capacity and owner approval remain release gates. M3–M5
remain in the original roadmap; this foundation does not complete that scope.
