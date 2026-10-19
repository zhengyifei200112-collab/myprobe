# Dashboard event processing

The public dashboard previously serialized its entire reactive node array into
`localStorage` after every per-node WebSocket event. With many Agents, heartbeats,
reports and target results repeat that work even when a newer event immediately
supersedes the previous cache entry. Card and table rerenders also repeatedly
constructed locale formatters for unchanged sample/contact timestamps.

## Behavior

Node events still update rendered state immediately. The optional `myprobe-nodes`
fallback cache now serializes the latest array once per pending 250 ms window.
The first event fixes the deadline, so a continuous stream cannot postpone writes
indefinitely. Idle pages do not write. The cache does not retain an earlier node
array in a delayed closure.

HTTP loads and complete WebSocket snapshots write immediately and cancel any
pending write. This includes empty snapshots and snapshots that remove hidden
nodes. `visibilitychange` to hidden, `pagehide` and component disposal flush any
pending current state. Disposal cancels timers; storage/quota failures do not
interrupt rendering or subsequent valid events. Browser timer suspension can
delay an ordinary pending write; this cache remains an optional fallback rather
than a durable event log.

Capture/contact text keeps the exact `zh-CN`, 24-hour locale output on cache
misses, with the existing independent missing/invalid placeholders. A shared
256-entry FIFO cache reuses text for the same local civil second. Keys use local
year/month/day/hour/minute/second, including after a default-timezone change;
the implementation does not retain an `Intl.DateTimeFormat` with an old timezone.
No timestamp, liveness, report cadence, API, database or Agent protocol changes.

## Validation

`npm --prefix web test` includes deterministic tests for delayed serialization,
continuous-stream deadlines, immediate empty/privacy snapshots, disposal,
storage failures, bounded formatting reuse and byte-identical locale text across
UTC, Shanghai, New York DST transitions and the Chatham fractional offset.

The existing disposable-Server dashboard browser check additionally sends 500
synthetic node events to the committed UI. It verifies immediate convergence of
all 50 cards before a single delayed serialization, current cache contents,
privacy/empty snapshots, hidden/pagehide/reload flushing, quota recovery and
clock-only timestamp reuse. Existing light/dark, 360/768/1440 px card/table checks
continue to run. Its source manifest includes the new helpers and tests.

## Performance evidence and limits

Before this change, an isolated native Linux diagnostic replayed 2,375 synthetic
WebSocket deliveries across five real Chromium pages per 30-second trial, using
50 nodes and the committed parent UI. Two rotated rounds compared unchanged
behavior, timestamp memoization, omitted full-array serialization and both.
Average aggregate CPU was respectively 22.06, 19.41, 14.10 and 12.03 seconds.
Every trial converged all five pages and 50 nodes without a page error. Omitting
serialization was an exploratory upper bound, not this production implementation.
The private result and committed asset hashes were independently verified.

These component diagnostics use synthetic API/WebSocket data and no actual
Server or Agent. They do not establish the joint-load or 24-hour acceptance
criteria. In particular the parent candidate's fresh dense-year native 50-node
joint run failed the 30-day history gate (P95 3,164.40 ms versus <2,000 ms), despite
passing report accounting, rendered convergence and the overview/RSS gates.
This UI change requires a new source-bound joint run; no prior result is relabeled.
