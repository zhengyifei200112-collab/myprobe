# Retained latency history covering reads

Dense history can read a correct node/target/kind/tier/time range and still
perform a second B-tree lookup for every retained bucket. This is particularly
costly when switching between nodes whose pages are absent from the filesystem
cache. A small warm query does not establish the 30-day endpoint's capacity.

Migration `050_history_covering_reads.sql` adds
`idx_latency_rollups_history_cover`. Its first five columns are the existing
node, target, captured kind, retention level and bucket time. The four weighted
statistics follow them, so the retained part of the history query can read its
input directly from the index. The query still constrains both time bounds and
both supported levels. Migration050 leaves raw sample and metric/traffic queries
unchanged; [migration051](RAW_HISTORY_COVERING_READS.md) subsequently covers the
raw projections while preserving their statistics and time bounds.

The existing unique constraint, foreign keys and retention indexes remain.
Weighted counts/sums, successful null latency, historical target kinds, disabled
or unassigned targets, exact windows, hidden-node scope and deterministic public
ordering keep their previous semantics. No API, Agent protocol, privacy field or
data-retention policy changes. The index stores values already present in the
database; it introduces no new logged payload.

## Upgrade and storage

This is an additive, forward-only index migration. Index creation and its schema
receipt commit together; a failed receipt rolls back the index. Reopening a
successfully upgraded database does not rebuild it. Existing aggregate rows are
neither rewritten nor deleted. Subsequent retention inserts, updates and deletes
maintain the additional index through SQLite.

The index duplicates its keys and four statistics, increasing disk use and the
work done by retained-data writes. Its size depends on the number of retained
targets/buckets and the lengths of their identifiers. Building it also requires
temporary sorting/journal space and holds a SQLite writer. It is a startup
migration, with no promise of an online or instantaneous upgrade. Before upgrading
a populated installation, keep a verified backup, check actual free space and
allow time for index creation. Keep the normal backup/restore procedure; do not
delete retained history or change its density to make the migration fit.

Older application code can ignore the additional index. An application downgrade
does not remove it or reclaim its space; do not edit an applied migration.

## Validation and limits

Planner regression tests require a covering time seek for short, 30-day and
one-year windows. They fail on the previous query even though that query already
has a time-bounded index seek. Populated-schema tests exercise failed-receipt
rollback, repeated migration, preserved weighted/null/kind results, and updates
and deletes after index creation. Existing differential history tests also run.

An independent restored synthetic corpus can attribute read costs and compare
result hashes, with the original DB/WAL/SHM archive left untouched. Such a
component experiment is distinct from the full endpoint and from concurrent
Agent/report/retention load. Its timings must include the actual resource limits,
cache preparation, index-build duration and extra disk use. A component speedup
does not accept the original failed 20-node run or waive the required final-source
five-to-twenty short run, uninterrupted 24-hour run, memory trend or recovery gates.
