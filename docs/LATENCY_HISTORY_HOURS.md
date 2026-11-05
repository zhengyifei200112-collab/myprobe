# Hour summaries for retained latency history

Long history windows now read weighted hour summaries for complete interior
hours. This reduces the retained bucket values read for the 30-day and one-year
views. Raw samples, partial boundary hours, and legacy timestamps that are
noncanonical or unaligned keep the existing source query rules.
Buckets predating the Unix epoch also use the source path, so migration
backfill cannot fold a negative timestamp into the first supported hour.

Migration 052 builds the summary from existing retained buckets and creates the
invalidation triggers in the same migration transaction. Each summary stores
sample, success and latency counts together with the latency sum. Missing
latencies and failed samples keep their original weights. Queries continue to
enumerate historical target kinds and apply the same node visibility check.

## Source changes and refresh

Source inserts, updates and deletes remove each affected canonical hour summary
and record the hour as pending in the same transaction. The insert trigger runs
before SQLite resolves a replacement, including when recursive delete triggers
are disabled. An ignored or rolled-back source operation can cause additional
refresh work, while the query continues using current source rows.

Queries combine valid summaries with the source buckets for pending interior
hours. Both sources and the pending markers come from one SQLite statement
snapshot. A reader cannot observe a newly changed bucket together with its
previous summary. The pending source query seeks by node, target, captured kind,
retention tier and hour bounds.

The existing bounded retention transaction rebuilds at most 128 pending
node/target/kind/hour groups, then removes those markers. Remaining markers make
the retention worker schedule another pass through its existing catch-up loop.
A failed pass rolls summary publication and marker removal back together.
The existing all-at-once retention operation refreshes all pending groups before
its commit. Rebuilding from source sums avoids repeated floating-point delta
adjustments.

Deleting a node or target cascades through its summary rows. Pending tombstones
can remain briefly until the refresh operation removes groups with no source.
They contain only identifiers, captured kind and hour.

## Upgrade and storage

The new summary and pending tables are stored in the same SQLite database.
Canonical retained buckets remain available for boundaries and refresh. The
additional partial covering index contains the existing noncanonical or
unaligned retained rows used by the compatibility path.

The migration backfills populated installations while holding the SQLite
writer. Check available space and keep a verified backup before upgrading.
Allow for the new summary pages and migration sort/journal space. Subsequent
retention writes invalidate affected groups and refresh them in bounded passes.
Actual migration duration, space and concurrent report cost require measurements
on the final candidate.

Applications that predate 052 still read the retained source tables. Their writes
also fire the invalidation triggers. On returning to current code, pending
groups use source reads until retention refreshes them. An application downgrade
keeps the added tables, index and triggers.

## Validation

Differential tests compare windows with the previous weighted query before,
during and after partial refresh. They cover nanosecond boundaries, unaligned
legacy starts, successful samples without a latency, replacement with recursive
delete triggers disabled, moved source buckets, source deletion, transaction
rollback and parent cascades. Migration tests cover populated backfill, receipt
failure rollback and repeated opening. Query-plan checks require bounded seeks
through summary, pending and compatibility paths.

The final-source 5-to-20-node short workload and 20-node 24-hour run remain the
capacity acceptance gates.
