# Latency history aggregation

The native 50-node progression failed before the actual Agent/browser measurement:
the first one-year history request exceeded the fixture's existing 10-second HTTP
client budget. A source-bound diagnostic Server on a separate copy reproduced
the timeout. Metric, latency and traffic stages took approximately 0.74, 7.99 and
1.27 seconds. The sampled pool wait count stayed zero during that request and
20 concurrent health samples succeeded. These observations narrow the next
query optimization; they do not establish the cause of every previous timeout.

## Change and preserved behavior

`latencyHistoryQuery` groups raw samples and retained rollups independently by
the requested output bucket, target and kind. It then combines sample, success
and latency counts and latency sums, and joins target names/order only after the
large input has been reduced. This avoids carrying/repeating target metadata
through every original sample and rollup row.

Average latency remains total successful non-null duration divided by its count;
success rate remains total successes divided by all samples. A successful sample
with a missing duration contributes to success rate but not average latency.
Failed samples do not contribute their duration. All-failed and all-null groups
retain a null average, and unequal source weights are retained. As with ordinary
floating-point sums, regrouping can affect the last rounding bits; tests compare
numeric results with a tight tolerance rather than promising arbitrary floating
point expressions are byte-identical.

Both source branches apply the existing public/admin visibility condition,
nanosecond raw bounds and complete-rollup boundaries. The statement keeps one
SQLite snapshot and the existing bucket/target ordering. There are no new API
fields, protocol changes, migrations, indexes, retention/pool settings or timeout
allowances. Metric and traffic queries are unchanged.

## Evidence and remaining acceptance

Before implementation, the old query and two alternatives were compared on the
same owned native working copy with the actual SQLite 3.50.4 engine, 2 CPU quota
and 2 GiB memory cap. Three rounds rotated variant order. All 18 observations
retained the same per-range row count and full result digest. Mean latency-query
times for the selected source-grouping alternative were:

| Range | Previous query | Source grouping |
| --- | ---: | ---: |
| 30 days | 687.93 ms | 461.38 ms |
| One year | 2420.82 ms | 1653.17 ms |

This is a small same-copy SQL comparison, with page caches warmed by earlier
diagnostics. It is not HTTP p95, a cold-cache fix, a 50-node joint-load pass or
100-node/24-hour acceptance. The failed original corpus and its failure evidence
remain preserved. The production site has not been switched.

The regression fixture compares the old one-pass query with the production
implementation over mixed raw/minute/five-minute rows, fractional bounds,
multiple resolutions, target kinds and public/admin hidden-node scopes. Explicit
weighted expectations independently check failures, successful null durations,
unrelated nodes, empty ranges, target sorting/renaming and cancellation. Existing
history boundary, privacy and index-plan tests remain required.

After local baseline and exact-source CI, run the uninstrumented Server against
a separately hash-verified copy with the original 30-second readiness and
10-second HTTP gates. Then repeat the actual 50-node/five-browser progression
and its SLO/report accounting checks. Start the 100-node/24-hour run only after
that progression passes. Retain failures instead of changing gates or reporting
a warm request as cold acceptance.
