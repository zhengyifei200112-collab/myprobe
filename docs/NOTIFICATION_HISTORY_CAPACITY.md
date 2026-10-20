# Durable notification history growth

This opt-in Store workload grows **historical records, not node count**. The
owner's self-use requirement remains at most about 50 nodes; this fixture uses
four synthetic nodes and makes no Agent/browser or 50-node capacity claim.

The seed executes real Store observations, leases, final authorization and
completion for ordinary firing/recovery deliveries, three-member aggregates and
a backup-failure reminder. Transport success is a supplied test outcome: no
provider, HTTP sender, Telegram bot or external destination is contacted.

Copy that completed trace with all owned identity references rewritten to grow
1000/10000/100000 closed incidents. Each level also grows its linked deliveries,
attempts, frozen aggregate bodies, exact attempt membership, compatibility events
and audit records. Backup runs and system reminders separately exercise their
documented 1000-task bound and the protection of pending/inflight reminders. This is not 100000 real sends or
elapsed production history. Current nodes, rules, channels and actor snapshots
remain real fixture entities; no empty result placeholders replace full bodies.

At each level, run at least 100 observations per read family alongside real
Store.SaveReport calls scheduled every 20 ms for at least three seconds. Check
incident/delivery/attempt and aggregate pages, exact aggregate context, audit,
queue diagnostics and idle workers. Completed history must not become pending,
be claimed again or mutate. Report attempts, successful writes and final
cancellation are separate; these numbers are not Agent throughput or ACK proof.

Hash every typed column of all 10 seeded table subsets in primary-key order.
Reads, idle claims and concurrent reports must keep them unchanged. The seven
permanent incident/delivery/attempt/aggregate/audit subsets and the retained
compatibility-event subset must also remain
unchanged through fresh ordinary/aggregate/system cycles. Do not falsely treat
bounded backup history as permanent: new backup results may prune old completed
tasks according to [the existing policy](SCHEDULED_BACKUPS.md), while an oldest
future retry remains byte-for-byte intact. Normal metric retention advanced 400 days
must not alter any of the retained subsets. A consistent SQLite backup/reopen
must restore their exact hashes and the protected retry. Integrity and foreign-key
checks must pass. A new post-restore cycle must refer to its new work; the original
protected reminder must subsequently retry and complete once, not be lost or
claimed after completion.

The compatibility alert_events table has a separate 90-day cleanup in the legacy
RecordAlertAttempt API. This fixture does not call that legacy API or promise
indefinite retention for its rows; it checks the retained compatibility data
through the specific lifecycle, metric-retention and restore operations above.

Times are Store-call nearest-rank p50/p95/max with sample counts. Database,
allocated SQLite pages and end-of-level WAL size are separate; end WAL is not a
peak. These measurements do not replace the unchanged overview/history/RSS SLO,
continuous 24 hours, encrypted actual-application restore, or real-provider tests.

## Run locally

Set an absolute, existing private evidence directory. Result creation is
exclusive. Failed synthetic fixture directories remain there for inspection;
successful owned temporary databases are removed after all checks.

```powershell
$env:MYPROBE_TEST_NOTIFICATION_HISTORY_CAPACITY='1'
$env:MYPROBE_TEST_NOTIFICATION_HISTORY_OUTPUT='<absolute existing directory>'
go test ./internal/store -run '^TestNotificationPermanentHistoryCapacity$' -count=1 -timeout=15m -v
```

MYPROBE_TEST_NOTIFICATION_HISTORY_MAX may be 1000 or 10000 for a smaller local
development check. Omit it for all three levels; a smaller run is not the full
100000-record result. The test is skipped in the ordinary baseline.

The Linux CI helper builds the exact clean checkout, checks all source bytes
against Git blobs and records the executable hash. A private-network systemd
unit limits the complete Store process to 2 CPUs / 2 GiB / no swap and CPUs 0–1. It
records the live executable/namespace/limits, resource observations, terminal
state, and unchanged source/program hashes. The host's actual memory is recorded;
an aggregate cgroup is not a physical 2 GiB VM. Only screened JSON is uploaded;
ephemeral CI databases and logs are excluded. A failed CI fixture lasts only as
long as that runner, unlike the explicit local output directory.

## Local and native component results

The complete Windows run passed in 67.62 seconds (Go 1.26.5, CGO=0, unrestricted
local CPU/memory). These are Store-call measurements, not application SLOs:

| Historical incidents | Allocated SQLite bytes | Largest read-family p95, ms | Successful report writes | Interrupted at shutdown |
| --- | ---: | ---: | ---: | ---: |
| 1000 | 7495680 | 2.03 | 150 | 0 |
| 10000 | 63897600 | 2.06 | 149 | 1 |
| 100000 | 626946048 | 2.01 | 150 | 0 |

All old permanent hashes, retained backup snapshots, protected pending work,
fresh post-restore processing, integrity and foreign keys passed. One write was
canceled during explicit shutdown; it is not reported as a successful write.
The initial large fixture incorrectly treated backup tasks as permanent; their
documented 1000-task pruning explained the failing assertion. Those failed
fixtures remain in the private local validation output, and are not product bugs
or successful large-run evidence.

The native job for test source 7540c71 passed, and its four screened JSON files
were independently downloaded and verified. All 856 source files match Git;
the live test executable hash, resource limits, private network, successful
terminal unit, unchanged inputs and all 50 resource observations were checked.
Go was 1.26.9 linux/amd64. The host had 16,760,340,480 bytes of physical memory;
the Store process had the stated 2 CPU / 2 GiB / no-swap budget, with no sampled
OOM events. See the [native job](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/39389850943/job/118518441789).

| Historical incidents | Allocated SQLite bytes | Largest read-family p95, ms | Successful report writes |
| --- | ---: | ---: | ---: |
| 1000 | 7512064 | 2.75 | 150 |
| 10000 | 64073728 | 2.42 | 150 |
| 100000 | 627290112 | 3.21 | 150 |

All native preservation, pruning/protection, post-restore retry and integrity
checks passed. No native report write failed or was interrupted. Independent
fixtures have different random identities and page allocation; hashes must
match within each fixture, not across operating systems.

The complete local ordinary Go/vet/build baseline and Node 22 ci/24 tests/build
also passed, with identical embedded assets. No application/schema/retention
policy change is included. The frozen application run remains at runtime
0d2d739; final 50-node/24-hour, physical-host, provider and user acceptance are
independent requirements. This document follow-up changes no test or runtime code.
