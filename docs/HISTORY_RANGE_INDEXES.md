# Bounded rollup reads

History range queries now explicitly enumerate the two stored rollup levels,
60 and 300 seconds. Both values are already enforced by migration 010's table
constraints. This equivalent predicate lets SQLite use the existing
`(node_id,bucket_seconds,bucket_at)` indexes for both dates. Previously, all three
production history queries selected only the node prefix of the epoch index and
filtered the requested dates after scanning that node's full retained rollups.

This changes index access, not the returned interval or statistics. Raw sample
nanosecond bounds, complete-retention-buckets-only selection, traffic ordering,
sample weights and public/administrative visibility remain unchanged. There is
no new index, schema migration, API field, Agent message or frontend asset.
Supporting another stored rollup level in a future migration also requires
updating this enumeration and its boundary tests.

Regression tests explain the actual metric, latency and traffic SQL and require
node, level and both date bounds in the index seek. The old production predicate
fails all three; the bounded predicate passes alongside existing fractional-time,
complete-bucket and visibility tests. Latency and traffic SQL have been named
privately so the tests explain the same statements used at runtime.

An earlier bounded-retention candidate's online-backup CI run had 30-day history
P95 2061.55 ms against an unchanged 2000 ms limit, with zero API errors. That
failure remains recorded. The query-plan defect is independently reproduced;
the CI result alone does not prove its individual cause or a performance ratio
between different hosted runners. Same-database SQL comparisons and exact-source
online-backup acceptance must be reported separately. A query-plan pass is not
the full-system 24-hour or hardware acceptance.
