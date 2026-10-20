# Node-batch preview expiry and replay retention

Pending previews are limited to 100 per user and expire after ten minutes.
Applied previews retain their frozen idempotency result until strictly older
than thirty days. These are bounded request records, not permanent history.
Creating a preview performs the existing cleanup transaction before checking
the per-user quota. Applying a preview remains atomic and replays the original
result without reapplying its node changes.

UTC RFC3339Nano strings have variable fractional width. Lexical comparison can
delete a `.5Z` preview when the current time is the earlier whole-second `Z`,
or retain an expired `.5Z` preview at `.500000001Z`. The same comparison could
prune an applied result before its real thirty-day boundary. Cleanup now
compares fixed-width UTC nanosecond keys derived only for the query. It does
not rewrite stored timestamps or introduce millisecond rounding. Pending
expiry is inclusive, matching the existing apply check; applied-result
retention remains strictly older than thirty days.

The regression tests use two synthetic nodes, not another capacity profile:

- 100 unexpired pending previews block the 101st for that user; another user
  can create a preview. At exact expiry, one nanosecond later and half a second
  later, expired records release their quota and a later unexpired record
  belonging to the other user remains.
- A still-valid fractional-second preview survives cleanup and can be applied.
- Frozen results and key conflicts remain valid immediately before and at the
  thirty-day boundary, and are cleaned after that boundary.
- A consistent SQLite snapshot/reopen preserves the 100 pending records, the
  frozen applied result and its single audit. Expiry cleanup after reopening
  releases pending quota without replaying the applied operation.

Run `go test ./internal/store -run '^TestNodeBatch' -count=1`. Existing migration,
ownership, atomic rollback, target epochs and heartbeat tests remain relevant.
This is request-ledger correctness and snapshot coverage. It does not establish
twenty-node throughput, an encrypted application restore, real Telegram delivery
or continuous twenty-four-hour acceptance. The native candidate must keep its
own source/binary binding when this runtime change is integrated.
