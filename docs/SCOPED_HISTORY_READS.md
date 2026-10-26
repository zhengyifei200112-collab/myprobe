# Scoped history reads

The checkpoint below records the original shared-pool implementation and its
historical comparison. Later migrations050/051 cover the current projections;
[short-lived file history readers](HISTORY_READ_CONNECTIONS.md) now provide a
separate two-connection read-only pool and the mapped/buffered-read option. The
current personal-use acceptance scale is five-to-twenty short checks followed
by twenty nodes for24hours; historical50/100 runs below do not change that scope
or grant the current candidate a pass.

The native `84e27cd` fifty-node/five-page dense-year run completed its functional
paths, but thirty-day history P95 was 5342.27 ms, above the fixed 2000 ms gate.
The capacity acceptance therefore failed. The load harness now enforces that
gate; this change addresses the reads themselves without changing the limits.

Latency queries enumerate target IDs and both retained `ping`/`tcping` kinds,
then seek existing indexes with all preceding keys and both time bounds. Raw
samples use migration 041's `(node,target,captured_sort_ns)` index; retained
samples use migration 010's `(node,target,kind,tier,time)` primary index. This
avoids traversing a node's full year or interleaving cold table lookups from the
date-only access path. Target names are still joined after per-source reduction.

Current node assignments, enabled flags and current target kind do not decide
which historical series exist. The query includes all target IDs, both retained
kinds, original raw nanosecond bounds, complete-rollup predicates and public/admin
visibility. It combines sums/counts before division, retaining unequal weights
and successful samples without durations. No schema, index build, protocol,
retention policy, pool size, request budget or response field changes.

The handler overlaps latency with sequential metric/traffic reads. Across all
node-history requests on a Server, at most two such queries enter the shared
four-connection Store at once, leaving slots for other application work. Queued
reads observe request cancellation. The additional reader is always joined,
including failure and panic paths; its unexpected panic becomes a generic API
failure. Gin context/response writes remain in the request goroutine. The same
absolute window is passed to each read. As before, these are three independent
read snapshots rather than one cross-series database transaction.

The development comparison used a fully rehydrated/hash-verified disposable
processed synthetic fifty-node copy, SQLite 3.50.4, kernel-enforced read-only
access and 2 CPU/2 GiB/no swap. Each observation opened fresh connections and
requested eviction of that copy's clean pages. Three node positions, three
rotated rounds and six strategies produced 54 full three-series comparisons;
every digest matched its node's baseline. Current serial reads averaged 2422 ms,
target/kind seeks 1410 ms, and those seeks with two overlapping reads 993 ms
(maximum 1098 ms). Rowid-order and primary-prefix alternatives did not meet the
goal and remain negative diagnostic evidence.

Those observations had no Server/Agent/browser workload and are not HTTP P95,
the final production query, or a fifty-node capacity pass. Full actual-main cold
HTTP checks, final-source CI and a fresh fifty-node joint run must verify the
candidate. Only after the original gates pass may the hundred-node/24-hour run
proceed. Physical 2-GiB VM, real providers and release approval remain separate.

Differential tests cover weights, failures/null durations, both stored kinds,
fractional boundaries, hidden/admin scopes, disabled/retyped/unassigned targets
and current metadata/order. Plans must seek both raw and retained time bounds
for short, thirty-day boundary and yearly windows. Admission tests cover shared
limits, overlap, cancellation, errors and panic cleanup; existing HTTP tests
cover unchanged payload, absolute windows and public/admin privacy.
