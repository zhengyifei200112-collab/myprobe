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

Windows of thirty days or less keep the existing node/level/date index seeks.
Longer windows use the existing rollup primary index in node/target/kind/level/time
order. This trades a wider scan of the node's index for more sequential table-page
access when much of its year is needed. Time and complete-bucket predicates still
filter the same rows. No other node's history is traversed. The primary-index name
comes from released migration 010 and is verified by a latest-schema query-plan
test; future table rebuilds must preserve or update that access contract.

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

## Cold access follow-up

The first implemented aggregation-only source (`3c5983a`) passed its full local
baseline and all ten CI jobs. Its uninstrumented first-read probe still timed out
at one year under the unchanged ten-second budget. The thirty-day response took
2.91 seconds and all 714 complete buckets checked correctly; thirteen concurrent
health observations succeeded. This failure and its working copy remain retained.

A subsequent read-only comparison used that quiescent working copy, fresh SQLite
connections and an advisory eviction request before **each** observation, under
the same 2 CPU / 2 GiB cap. Three alternating rounds preserved the complete output
digest and row count. The one-year latency query averaged 6589.31 ms with the date
index and 1999.34 ms with the node's primary-index order. Actual process read bytes
increased from about 126 MB to 189 MB, while CPU and wall time decreased. A second
three-variant comparison also evaluated explicit series/time seeks; those averaged
6235.97 ms for one year and were not selected.

These measurements support the long-window access choice, not a guarantee of a
cache state, an optimal crossover for every dataset, or a passed HTTP/capacity gate.
Short windows retain selective date access to avoid broad index traversal. New
weighted/privacy/boundary tests exercise both paths, including exactly thirty days,
one nanosecond longer and one year. The combined production reader still needs a
fresh uninstrumented HTTP probe and the full native 50-node progression before the
100-node continuous run starts.
