# Raw metric, latency and traffic covering reads

The 050 retained-latency index reduced one component's read cost, but the first
native five-node whole-workload check still exceeded the original 2-second
30-day history P95 gate. Correct reports and a faster isolated component do not
accept the full workload. Raw samples still required table-column reads for
their projected metric, latency and traffic values.

Forward migration `051_raw_history_covering_reads.sql` adds two indexes:

- `idx_metric_samples_history_cover`: node, normalized capture time, original
  integer row ID, then timestamp, metric values and network counters. Metric and
  traffic history share this index. The opaque report JSON is excluded.
- `idx_latency_samples_history_cover`: node, target, normalized capture time,
  original integer row ID, then timestamp, captured kind, success and latency.

The history queries retain their existing bounded time predicates and use
these indexes explicitly. Row ID precedes payload columns so equal-time samples
keep the previous index's row order; payload values must not reorder floating
sums. Raw traffic still applies its exact-time/text/row-ID ordering and reset
calculation in Go. Existing latest-row, uniqueness and retention indexes remain.

No API, Agent protocol, visibility rule, raw/retained lifetime or billing query
changes. Captured kinds remain historical even if a target is later disabled,
renamed, retyped or unassigned. Rollup counts, weights, complete-bucket policy,
half-open windows, null latency and hidden-node access keep their previous
semantics. These indexes contain data already stored locally; no new public
fields, log payloads or credentials are introduced.

## Upgrade and storage cost

Both index creations and the migration receipt use the existing one migration
transaction. Failure during the second index or receipt insertion rolls the
transaction back. The migration does not backfill or rewrite raw sample values;
subsequent inserts, updates and deletes maintain both covered values and the
generated capture-time keys. A second successful migration call skips the
already recorded migration.

The new indexes consume disk and add raw-write work. They also require startup
writer time and temporary sort/journal space while building over existing
history. Check actual free space and keep a verified backup before upgrading a
populated installation. Index size/build duration depends on raw rows and key
lengths; do not reduce history density or change retention to make an upgrade
fit. Existing schema migrations, including050, remain immutable.

Older query code can continue using the retained original indexes; extra index
space is not reclaimed by an application downgrade. Follow the normal matching
program/database backup and rollback procedure. No production cutover or owner
merge is authorized by a successful component test.

## Validation boundaries

The original query fails the planner regression for raw-table `Column` reads
(10 metric, 5 latency, 3 traffic instructions). The changed query must eliminate
those raw-table projection instructions while retaining node/target/time seeks
for short, 30-day and year windows. A generated index key may leave a table
cursor open without EXPLAIN QUERY PLAN calling the access covering; the check
uses actual typed VM operations, not arbitrary P1-number matches or a timing
threshold on CI hardware.

Exact new/old-index differential tests include fractional/equal timestamps,
weighted retained layers, null/failed latency, captured kinds, visibility,
counter resets and both generated-key/payload updates followed by deletion.
Independent expectations check same-time metric/latency means and traffic
deltas. SQLite's compensated SUM is respected; old/new result equality is not
replaced by a tolerance. Populated migration tests cover both rollback points
and repeated successful migration with every source row unchanged.

An independently restored, CRC/SHA-verified synthetic failure set may compare
exact results and costs without opening original failure bytes with SQLite.
Record actual limits, cache preparation, index-build duration and added disk.
Preserve any unknown full-integrity status. These component checks do not
substitute for a qualifying final-source five-to-twenty short run, uninterrupted
20-node 24 hours, steady memory, full aged reference, recovery or remaining
human/provider/release gates. The previous five-node failure remains failed.
