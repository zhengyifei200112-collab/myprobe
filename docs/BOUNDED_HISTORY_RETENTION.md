# Bounded host history catch-up

Server startup commits one closed raw minute, at most eight complete node/five-minute
groups of minute rollups, bounded expired five-minute pruning (100 rows per
table), and a separate existing HTTP batch. It then opens the listener. Startup
does not wait for the entire historical backlog. The background worker yields
100 ms between passes while either host or HTTP work remains; it retries errors
after five seconds and uses the configured interval once both are idle.

This addresses a reproduced 100-node aged, approximately 30 GB fixture: the
original single retention transaction took 131.68 seconds on its independent
private copy; raw latency aggregation alone took 35.97 seconds. That offline
profile is not a successful startup or proof of an earlier long-test failure.
The first prototype became ready in 6.71 seconds but failed a subsequent one-second
health request during catch-up. Three instrumented passes on that post-failure
copy took 0.84–0.94 seconds, including 0.33–0.37 seconds per COMMIT. The current
smaller minute/expiry quanta address this write amplification; they still require
a fresh native acceptance run. The readiness acceptance remains 30 seconds.
A time window bounds the declared
probe workload, not arbitrarily dense imported data or a wall-clock deadline.

## Data consistency

Each host pass reserves the SQLite writer before selecting work. It uses the
existing aggregate, traffic counter reset, bucket identity and pruning rules.
Rollups, covered-row deletion and counter-anchor updates commit together. It
never promotes a five-minute group while eligible raw minutes in that group
remain. Readers can continue querying unprocessed raw and minute history.

Migration 049 adds only a small `history_retention_anchors` table, with at most
one retained sample identity per node. It does not scan/backfill raw history or
build another large index. Anchors remain traffic LAG input, but do not endlessly
select themselves as pending work. They survive reopen/backup/restore with the
database; node deletion cascades, and sample deletion clears the reference.
An empty marker table safely initializes from surviving samples, including
databases previously processed by the full retention method.

Statement errors/cancellation roll back the pass. If COMMIT itself fails, the
connection stays reserved until an explicit native rollback; an unusable
connection is discarded. No partial successful batch is reported. Earlier
committed batches remain durable after interruption, and restart resumes from
the actual remaining rows rather than a guessed time cursor.

`retention.job` is the outcome of the most recent host pass. Success means that
pass committed; it does not certify that the complete backlog has drained.
`retention.worker` continues to show the real scheduled retry/catch-up delay.
There is no new public field, Agent protocol, API contract or Provider action.
The full `Store.ApplyRetention` method remains for explicit maintenance and
existing comparison/backup fixtures; Server startup and its worker use batches.

## Upgrade and validation

Take a consistent backup and retain matching application keys before upgrading.
049 is forward-only and atomic with its migration receipt. Earlier migrations,
including 048's index creation, still have their own upgrade I/O costs. To roll
back, use a separate copy of the pre-upgrade backup and its matching application;
do not delete migration records in a live database.

Regression tests compare a drained batch sequence against the original complete
transaction across raw/minute/five-minute tiers, gaps, counter resets and
fractional timestamps, including reopen midway. They inject statement and
deferred-constraint COMMIT failures, check cancellation and bounded expiry,
migration failure/idempotence/no backfill, and independent host/HTTP catch-up.

The required native startup/restart and continuous run now target the owner's
20-node acceptance target (usual deployment about 15), confirmed on 2026-10-20. Aged-backlog rehearsals still require
a fresh verified copy of their declared input; a previously processed diagnostic
copy cannot replace it. The original 100-node failed experiments above retain
their source-specific conclusions and are optional for this release. Full backlog
catch-up and concurrent writes must be assessed against their actual fixtures;
neither short readiness nor unit tests prove final 24-hour, whole-machine
2 vCPU/2 GiB, real Provider, deployment or release acceptance. See the current
[candidate evidence and remaining gates](CANDIDATE_ACCEPTANCE.md).
