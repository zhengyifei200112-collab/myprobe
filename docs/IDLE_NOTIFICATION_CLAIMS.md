# Idle notification claims and writer contention

Notification consumers now use a read-only negative preflight before acquiring
the SQLite writer. When work may exist, the original transaction still recovers
leases, rechecks destination/state/suppression, and reserves the job atomically.
No worker polling interval, SQLite busy timeout, health deadline, retry budget,
attempt history, idempotency identity or Provider authorization has changed.

| Queue | Read-only miss | Original writer transaction remains required |
| --- | --- | --- |
| Single incident delivery | No due non-aggregate pending row and no expired non-aggregate lease | Any potentially due job or expired lease, including cancellation/retry handling |
| Aggregate delivery | No due pending aggregate and no expired aggregate lease | Any potentially due group or expired lease, including member/state checks |
| System notification | No pending row and no expired lease | Any pending row, even future work, since a stale channel must still be canceled before its due time |

A negative snapshot does not reserve or consume a job. Work arriving immediately
after that read is seen by the next normal poll. Every positive path retains the
existing serialized transaction; this does not promise delivery exactly once.
Paused/stale jobs may conservatively enter that transaction and receive the same
decisions as before. Invalid arguments and canceled contexts remain errors.

The old empty single, aggregate and system claim paths each waited behind an
unrelated held writer, exceeding their one-second caller contexts after about
five seconds. Four empty incident consumers also occupied all four primary
connections and starved an otherwise read-only health request in the regression.
The new negative paths return without taking that write reservation and keep
health available under the same fixture. No dedicated health connection or
weaker readiness check was introduced.

Tests retain the old failing sources/evidence, check empty/future/unexpired
misses under a held writer, preserve due/expired recovery, and verify future
system notifications still cancel stale destinations. Existing concurrent
claims, retry budgets, final authorization, ambiguous sends and actual-main
delivery lifecycle tests remain required. There is no migration, new public
field, Agent protocol or frontend change.

Cold 100-node retention diagnostics independently observed all four Server pool
connections occupied and a separate synthetic writer's node initialization
failing with SQLITE_BUSY. That run ended before reproducing the earlier health
timeout. The focused regression proves this idle-poll contention mechanism; it
does not retroactively prove every earlier failure's individual cause or complete
the fresh native/full-system/24-hour acceptance.
