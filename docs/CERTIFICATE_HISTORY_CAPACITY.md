# Permanent certificate history and current alert work

Closed reminder identities survive service deletion and retention so a previous
send cannot be mistaken for a new reminder. That history must stay readable
without making every current evaluation rewrite all inactive definitions.

## Reproduced problem

On Windows/amd64, Go 1.26.5, the unchanged 2d7180f runtime processed a disk-backed
synthetic history at 1k/10k/100k identities. Full `Service.Tick` calls grew from
about 0.05 seconds at 1k to 4.29–5.15 seconds at 100k. With concurrent production
`Store.SaveReport` and the normal SQLite busy timeout, a separate run recorded
46 attempts, 44 committed rows and 2 `SQLITE_BUSY` errors; full ticks took
4.52/5.89/5.60 seconds. The permanent identity/rule digest stayed unchanged.

An earlier experiment with an explicit five-second diagnostic operation context
recorded one context deadline; that is separate from the default-timeout result.
These are local component observations, not transport loss, native target
capacity or p95. The input was SQL copies of a real expired/deleted-service
certificate identity, not 100k real rotations. A normal regression also fails on
the old runtime because a service rename rewrites a closed, disabled definition.

## Current behavior

- The evaluator requests enabled rules; administrative rule/history reads retain
  disabled records. Incident reconciliation still runs before evaluation.
- Definition synchronization selects unskipped identities and enabled certificate
  rules. Paused current definitions still track settings, and a skipped identity
  with a stale enabled rule is still disabled. Closed disabled definitions keep
  their historical fields and timestamps.
- A service loads unskipped subjects plus every permanent level of its currently
  observed certificate fingerprints. This preserves reappearance deduplication,
  conflicting validity-period rejection, authorization and unknown receipt rules.
- New identity admission keeps the global 100k cap. No identities, receipts or
  history are deleted, truncated or silently skipped to fit the current workset.
- Forward migration 048 adds two partial indexes. No public fields, protocol,
  dependency, generated frontend assets or production timeouts change.

Take a complete upgrade backup before migration. Index creation has I/O and write
lock cost, even when most historical rows are outside the partial index. A rollback
uses its separate original database and matching old application. Tests check
receipt-failure rollback, original history preservation and idempotent migration.

## Repeatable capacity fixture

`TestPermanentCertificateHistoryCapacity` is opt-in. It creates one certificate
through the real service/result/evaluation path, expires it and deletes its source
service. Bounded SQL batches clone that valid closed identity/definition at
1k/10k/100k. A separate enabled CPU rule remains in the evaluator's current workset.

Each level runs three full ticks while another goroutine calls normal
`Store.SaveReport` without a custom per-write deadline. Every attempted report
must commit, stored-count delta must match, and the complete historical identity
and certificate-rule digest must remain identical. The fixture then checks normal
retention and a consistent backup/reopen, including decrypting its synthetic
channel with the original key. It never runs delivery workers or a real Provider.

```sh
MYPROBE_TEST_CERTIFICATE_HISTORY_CAPACITY=1 \
MYPROBE_TEST_CERTIFICATE_HISTORY_OUTPUT=/absolute/existing/aggregate-directory \
MYPROBE_TEST_SOURCE_COMMIT=full-frozen-commit \
go test ./internal/alerts -run '^TestPermanentCertificateHistoryCapacity$' \
  -count=1 -timeout 3m -v
```

CI runs the fixture and private rule/history race regressions. Only the screened
`certificate-history.json` enters its artifact allowlist; temporary databases and
fixture credentials do not. The JSON binds actual key sources and the driver.
Three tick durations are individual measurements, not p95 or a performance ratio
between different random corpora. File-backed Store/engine results do not establish
real Server API latency/RSS, whole 2-vCPU/2-GiB SSD capacity, all permanent ledgers,
arbitrary retention backlog, actual HTTP restore or 24-hour stability.
