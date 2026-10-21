# Self-use capacity and acceptance

The owner clarified on 2026-10-20 that MyProbe is for personal use, normally
about **15 nodes**, with **20 nodes** as the acceptance target. This supersedes
earlier plans that made 50- or 100-node acceptance
a prerequisite. Develop and validate the existing daily monitoring, history,
notifications and backup/upgrade workflows for this actual scale.

## Required profile

Progress from a five-node functional check to the current-source 20-node profile:

- Real Agent clients with 15-second reporting, three 60-second network targets
  per node, actual service execution and five rendered browser subscriptions.
- Dense retained history, report/ACK/store reconciliation, reconnect, Server
  restart, notification failure/recovery and recovery-queue drainage.
- A qualifying short run followed by an uninterrupted 24-hour measurement;
  preparation, synthetic data age and previous interrupted runs do not count.
- Overview API P95 below 500 ms, 30-day history P95 below 2 seconds, sampled
  Server RSS below 700 MiB, no unexplained missing reports or crashes, and no
  continuing steady-state memory growth.

The reference resource budget remains 2 vCPU / 2 GiB with SSD storage. Record
actual hardware, cgroup limits, filesystem/storage observations, all actors,
source and executable hashes, measurement boundaries and unavailable evidence.
An aggregate 2 GiB cgroup on a larger host is not proof of a physical 2 GiB VM
or of SSD performance. Keep the existing timeouts and strict SLO failure rules.

50/100/500-node workloads are optional expansion experiments, not delivery gates.
Earlier larger datasets can still exercise filtering, batch boundaries and
other correctness tests; they do not increase the owner's required node count.
This scope change adds no runtime node-count restriction or protocol change.

## Evidence and test storage

Previous results retain their original source, node count and limitations. A
failed 50-node P95 remains a failure; reducing the required maximum to
20 does not turn it into a pass. A run stopped for this scope change remains
interrupted and cannot count toward an uninterrupted 24-hour result. Component
diagnostics and green five-node CI
cannot substitute for the required 20-node joint run or its 24-hour follow-up.

Retired synthetic failure corpora may be relocated to recover test storage only
after all owning processes have stopped and the original DB/WAL/SHM byte set is
preserved. Keep source metadata/logs/results, hash each original file, retain a
verified compressed archive and record exact byte sets, hashes and recovery
locations. The owner explicitly chose on2026-10-20 to release old reproducible
synthetic test originals after full compressed-stream inflate/CRC/original-hash
verification, without waiting for a full independent restored filesystem copy.
This exception is limited to those synthetic test corpora. Real business data
still requires the normal independent backup/restore safeguards. Never open
original failure files with SQLite or checkpoint them while archiving. Any
diagnostic SQL uses a separate new copy. Close all owning processes/cgroups and
release closed test-file caches after each terminal run; clear verified redundant
or incomplete reproducible copies before accumulating another large test.

The actual20-node fixture correction and bounded import receipt/restore checks
are described in [fixture validation](SELF_USE_FIXTURE_VALIDATION.md).

Real GitHub login, the owner's Telegram channel, daily workflow review and the
old-site upgrade remain separate acceptance work. Repository-owner approval is
still required before merging and releasing the integrated version.
