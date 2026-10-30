# Bounded host history catch-up

Server startup commits one closed raw minute, at most eight complete
node/five-minute groups of minute rollups, at most 100 expired five-minute rows
per rollup table, and a separate existing HTTP batch. It then opens the listener.
The worker continues both host and HTTP backlogs, yielding 100 ms between passes,
retrying failures after five seconds, and using the configured interval when
both are idle. Each pass releases the SQLite writer before the next wait.

A time window bounds work at the declared probe workload, rather than imposing
a row bound on arbitrarily dense imported data or a wall clock deadline. The
existing startup, request, memory and capacity thresholds remain unchanged.
Personal-use acceptance is five-to-twenty short checks and a same-source
twenty-node continuous 24-hour run; earlier optional 50/100-node diagnostics
are not scheduled by this change. The earlier 20-node P95 failure stays failed.

## Data consistency and exact times

Each pass reserves the writer before selecting a window. Rollups, covered-row
deletion and traffic-anchor updates commit together. A five-minute group is not
promoted while eligible raw minutes in it remain. Whole groups are processed per
node so a pass cannot publish a partially aggregated group. Unprocessed raw and
minute history remains readable through the existing history APIs.

Raw cutoffs, traffic counter ordering and latest latency selection use the
existing fixed-width virtual nanosecond key. Original RFC3339Nano timestamps and
report JSON remain unchanged. Minute bucketing uses the true whole second,
avoiding SQLite's fractional-second rounding at a minute boundary. Inclusive
billing bounds and half-open history bounds retain their respective contracts.

Forward migration 049 adds only `history_retention_anchors`: at most one retained
sample identity per node, with deletion references enforced by foreign keys. It
does not backfill raw history or build an index over it. Anchors remain traffic
LAG input without repeatedly selecting themselves as new work; they survive
reopen and consistent backup. Existing migrations, including 050/051, are not
modified. Migration tests exercise the actual main predecessor with those
covering indexes already present.

Statement failure or cancellation rolls back the batch. If COMMIT itself fails,
the connection stays reserved until an explicit native rollback; unusable
connections are discarded. Failed batches report no successful work. Restart
resumes from committed database rows, without relying on a guessed time cursor.

## Operations and validation

`retention.job` reports the outcome of the most recent host batch; success does
not certify that the complete backlog drained. `http_catchup_job` observes the
separate HTTP batch, and `retention.worker` observes the actual loop and next
scheduled wake. There is no new public field, Agent protocol, API or provider
action. The full `Store.ApplyRetention` method remains for explicit maintenance
and existing comparison/backup fixtures; startup and the worker use batches.

Take a consistent backup with matching application keys before an approved
upgrade. Migration 049 and its receipt are atomic. Existing migrations retain
their own startup and index I/O costs. Rollback uses a separate pre-upgrade copy
and matching executable; do not remove migration records in a live database.

Small correctness fixtures compare a drained batch sequence against full
retention across raw/minute/five-minute tiers, gaps, fractional timestamps and
counter resets, including reopen partway through. They inject statement and
deferred-constraint COMMIT failures, cancellation, migration receipt rejection,
bounded expiry, per-node group splits and independent host/HTTP catch-up. These
also resume a consistent backup taken partway through catch-up and require the
same anchor identities and exact final history. These fixtures are not native
capacity, long-run memory or complete recovery evidence.
