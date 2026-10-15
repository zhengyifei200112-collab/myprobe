# Network archive capacity experiment

This work follows NET-01's bounded-retention and mixed-tier statistics gates.
The implementation is stacked on PR #72. The fixture is opt-in:

```powershell
$env:MYPROBE_TEST_NETWORK_ARCHIVE_CAPACITY='1'
$env:MYPROBE_TEST_SOURCE_COMMIT=(git rev-parse HEAD)
$env:MYPROBE_TEST_NETWORK_ARCHIVE_OUTPUT='D:/scratch/network-archive-capacity.json'
go test ./internal/store -run '^TestNetworkQualityArchiveYearCapacity$' -timeout 12m -count=1 -v
```

Use a disposable volume with at least 4 GiB free. The test itself creates and
removes its file-backed SQLite databases. The optional output must name an
existing directory; it stores bounded JSON observations, not database contents.

The fixture generates 525,600 minute slots for one synthetic node/target. A
fixed 20-slot pattern contains execution failure, Agent refusal, dispatch-offline,
no task, and 16 successful RTT samples. Closed-form counts/jitter and a fixed
1,000-value integer frequency table independently predict the complete population
and exact nearest-rank quantiles; they do not reuse production merge logic.
Historical task rows are seeded directly through SQL
with valid result identities; this is not a year of Gateway/Agent runtime.

Measurements have two distinct phases:

1. The first 100 archival transactions use the production 100 ms inter-batch
   delay. Concurrent current manual tasks, accepted results and five-minute Store
   queries must succeed under an unchanged two-second operation deadline.
2. Those probes stop. The remaining historical rows are compacted as quickly as
   Store transactions permit, yielding the Go scheduler between transactions.
   This phase verifies full-year storage, summary populations and restore, and
   measures transaction cost. Its drain time is not production worker throughput
   and does not demonstrate concurrent writes throughout the full-year drain.

The first accelerated experiment used a 1 ms pause with continuous competing
writers and exceeded the two-second current-task deadline. Its first batches
were about 4–5 ms; query plans used the scheduled/manual retention indexes. That
stress setting differs from the production worker's 100 ms delay. Preserve this
failed experiment in the evidence; do not raise the operation deadline or report
unpaced draining as production concurrency acceptance. Production-paced results
must be recorded before making a claim about the actual worker.

After draining, the test checks every population and jitter count against the
full generated oracle and compares histogram bounds. It measures year and
30-day aggregate queries separately, creates a consistent SQLite snapshot,
stages and applies startup restore, and checks restored statistics and foreign
keys. It also asserts that the unarchived year exceeds the raw work budget with
an explicit error, rather than silently truncating a percentile population.

Resource observations distinguish allocated pages, reusable pages, file bytes,
summary payload bytes and compact snapshot bytes. Deleting rows does not itself
shrink the SQLite file. A 100 ms sampler records Go heap and observed WAL size;
neither is process RSS or a guaranteed exact WAL peak. This single node/target
case cannot satisfy the roadmap's 100-node/three-target/24-hour capacity gate.
The independent 24-hour runtime remains frozen on PR #70 and cannot validate
PR #71/#72 code.

## Measured Windows result, 2026-10-15

Fixture source `ecc525575a1f11070119cb008e6b40148eb0a865`, Go 1.26.5,
Windows/amd64, on the local i5-13490F workstation. The historical oracle contains
525,600 planned slots, 499,320 saved tasks, 420,480 successful and 26,280 failed
executions, 78,840 missing slots and 394,200 valid jitter pairs. The complete
mixed-tier population, histogram bounds, consistent snapshot, staged startup
restore and foreign-key verification passed. The raw year correctly refuses a
query above its work budget before compaction.

| Observation | Measured result | Scope |
| --- | --- | --- |
| Historical seed | 26.88 s | Synthetic task identities, one SQL seed transaction |
| Paced overlap | 100 batches / 10.37 s | Actual 100 ms production delay; current probes then stop |
| Current result writes | 103 samples, p95 1.664 ms, max 13.725 ms | Manual task creation plus accepted result; no deadline errors |
| Current 5-minute query | 10 samples, p95 0.628 ms | Store queries during paced overlap |
| Full drain | 15,305 batches / 77.13 s | Includes the overlap, then unpaced remainder; not production throughput |
| Largest transaction observation | 41.294 ms | One bounded archive call |
| Year / 30-day query | 673.31 / 186.46 ms | One aggregate Store request each; not HTTP or p95 |
| Restored year query | 460.44 ms | One isolated restored-DB Store request |
| Retained data | 9,575 raw / 31,465 minute / 96,480 five-minute rows | No fabricated empty buckets |
| Summary payload | 12,240,704 bytes | Excludes SQLite rows/indexes and other tables |
| DB allocated / reusable | 352,518,144 / 310,202,368 bytes | Physical allocation remains after deletion |
| Compact consistent snapshot | 39,309,312 bytes | Entire fixture DB, not encrypted backup transport |
| Observed WAL maximum | 355,712,592 bytes | 100 ms sampling, includes the large seed transaction |
| Observed Go heap maximum | 7,657,992 bytes | Whole test process heap; not RSS |

At 15,305 batches, production's inter-batch delay alone would add approximately
25.5 minutes. The complete production-paced year drain has not been timed; the
77-second fast phase must not be substituted for it. The high WAL observation
also includes the artificial one-transaction historical seed, not only routine
archival. Reusable pages are available for later writes; this test does not run
live VACUUM or claim that DELETE returns space to the filesystem.

Raw result: [Windows evidence](evidence/network-archive-year-windows-2026-10-15.json).
The failed 1 ms experiment and the adjusted phase contract are preserved in
[experiment metadata](evidence/network-archive-experiments-2026-10-15.json).
PR #72 implementation `ca9bfec` passed all steps of native CI
[38711368914](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38711368914);
this success predates the new capacity fixture and is not native capacity proof.
The new exact-source CI job independently runs the year fixture; its result is
pending. Full node/target combinations, actual worker-wide drain duration,
eight-node API/UI and platform/runtime acceptance remain open. Nothing here
merges, deploys, releases or completes NET-01/NET-02.
