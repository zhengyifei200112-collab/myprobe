# Report and retention writer contention

Report ingestion and bounded host-history retention begin deferred transactions
and reserve the SQLite writer before reading their working snapshot. The initial
reservation now retries exact `SQLITE_BUSY` (code 5) at most three times. Each
attempt retains the existing connection-level five-second busy timeout;
25ms and 50ms cancellable waits separate attempts. Caller cancellation and
deadlines take precedence. This is an attempt bound, not a hard real-time SLA.

The retry surrounds only the first write, which has not changed any rows when
writer acquisition fails. It never retries COMMIT, later statements,
BUSY_SNAPSHOT, LOCKED, constraints or arbitrary errors. Report capture times,
traffic accounting, deduplication, retention window atomicity and ACK-after-
commit remain unchanged. No database migration or protocol change is required.

File-backed tests hold a real competing writer for six seconds and exercise
report ingress and a bounded retention pass through the existing five-second busy timeout.
Report replay remains a single stored sample; cancellation, attempt exhaustion
and non-retryable error classes have separate checks.

This change does not provide an Agent durable pending queue or recover reports
after all attempts fail. It is not proof that every historical missing report
had the same lock cause, nor does it retroactively pass an earlier soak test.
Further ingress reliability and native capacity validation remain necessary.

The helper surrounds only the first UPDATE of each fresh deferred transaction.
The complete maintenance method ApplyRetention does not retry its business
statements. Normal startup/background retention uses RetainHistory and its
initial anchor-table writer reservation under the same bounds. See
[bounded host catch-up](BOUNDED_HISTORY_RETENTION.md).
