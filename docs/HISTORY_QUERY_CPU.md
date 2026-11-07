# Historical query CPU work

Current integration scope: personal use around15 nodes, required5→20 short test and same-source20×24h. The historical50-node investigations below are retained context, not a new workload or current acceptance. The original6d2297d20-node30d P95 failure remains unresolved; no threshold or native success is changed by these query improvements. Current-source complete tests and integration evidence are required.


The earlier development profile used50 nodes before the owner reduced required acceptance to20. Runtime 2257a2e completed
its native 50-node short workload with 999 matching report/ACK/store records,
all five rendered subscriptions converged, no API/page errors and drained
recovery. Overview P95 was 159.33 ms and sampled Server RSS was 92,200,960 bytes.
However, 30-day history P95 was 2283.63 ms, above the unchanged 2000 ms gate.
The short run failed and did not start the 24-hour follow-up.

Separate CPU profiles on a byte-verified copy of that synthetic failure corpus
show SQLite execution, record comparisons and sorting dominating the metric and
latency reads. This diagnostic has no live Agents/browser workload and is not
another capacity result. Original failure files are never opened with SQLite.

## Query changes

Every raw/retained source already constrains node_id to one bound argument.
Use an uncorrelated EXISTS against that same node for visibility instead of
repeatedly joining its metadata for each historical row. The node must still
exist for both public and administrator reads; administrators may include a
hidden node. All checks stay inside each statement's read snapshot.

Latency aggregation uses the targets table's integer row identity only as a
statement-local sort/group key. The final metadata join restores the public
target ID and name. No row identity is persisted, returned, used as a foreign
key or assumed stable across statements. Public ID/kind tie ordering is explicit.
Raw and retained kinds, target enumeration, successful-without-duration cases
and weighted sums/counts retain their existing semantics.

Earlier traffic CPU work replaced a database-wide text sort with an exact-time
Go sort. Subsequent bounded memory observations found that loading, sorting and
copying every raw counter made temporary allocations grow with input density,
even for a fixed sixty-point result. The range reader now orders counters in
SQLite by exact instant, timestamp spelling and original row ID, and folds each
adjacent delta directly into its output bucket. It retains only adjacent counters
and the result buckets in Go. Additive retained rows are folded as they arrive.

Both scans share one read transaction, preserving the original single-snapshot
semantics while retention atomically moves raw rows into retained buckets. A
concurrent move must not omit or count traffic twice. Counter resets, duplicate
spellings, exact nanosecond bounds, complete retained buckets and hidden-node
scope retain their original behavior. Cancellation closes the rows and snapshot;
file readers still close when returned to the zero-idle pool. Inclusive billing
queries keep their existing accounting path.

Local allocation observations distinguish heap allocations, heap after an
explicit diagnostic GC, anonymous RSS and file-backed RSS with mmap enabled and
disabled. Explicit GC is an observation intervention, not a production fix or
capacity criterion. These component results do not establish the cause of an
earlier full-run RSS trend or replace final-source continuous acceptance.

Existing node/target/kind/tier/time index seeks, exact raw nanosecond bounds and
complete-retention-bucket policy stay in place. The shared four connections,
global two-reader admission, cancellation/join, request timeouts, API, protocol,
schema, retention, browser updates and original SLOs are unchanged.

## Validation boundaries

Existing history/traffic/visibility/differential and query-plan regressions pass.
An additional regression changes internal target row identities, including
negative values and values greater than 2^53, while checking identical public IDs,
ordering, retained kinds and weighted points against the earlier reference.

The preceding candidate's native five-node short run still failed the unchanged
30-day gate at 2034.49 ms. Progression correctly left its 50-node and 24-hour
units unstarted. Its 54 component digests matched the earlier version, with
smaller metric/latency means but no traffic improvement; this motivated removing
the redundant traffic sort. That result remains a failure with its original
synthetic corpus preserved.

Current-source validation follows the [20-node acceptance scope](DEVELOPMENT_STATUS.md)
and [joint runtime procedure](JOINT_RUNTIME_LOAD.md): five-to-twenty short checks,
then a same-source twenty-node continuous 24-hour run. Historical fifty-node
investigations are optional context, not a required rerun. Component speedups or
green CI do not certify steady memory, physical 2 GiB SSD hardware, real provider
delivery or release. See also [previous indexed reads](SCOPED_HISTORY_READS.md).
