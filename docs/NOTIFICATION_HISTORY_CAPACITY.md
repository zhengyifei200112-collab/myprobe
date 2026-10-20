# Permanent notification history capacity

This opt-in Store workload grows **historical records, not node count**. The
owner's self-use requirement remains at most about50 nodes; this fixture uses
four synthetic nodes and makes no Agent/browser or50-node capacity claim.

The seed executes real Store observations, leases, final authorization and
completion for ordinary firing/recovery deliveries, three-member aggregates and
a backup-failure reminder. Transport success is a supplied test outcome: no
provider, HTTP sender, Telegram bot or external destination is contacted.

Copy that completed trace with all owned identity references rewritten to grow
1000/10000/100000 closed incidents. Each level also grows its linked deliveries,
attempts, frozen aggregate bodies, exact attempt membership, compatibility events
and audit records. Backup runs and system reminders separately exercise their
documented1000-task bound and the protection of pending/inflight reminders. This is not100000 real sends or
elapsed production history. Current nodes, rules, channels and actor snapshots
remain real fixture entities; no empty result placeholders replace full bodies.

At each level, run at least100 observations per read family alongside real
Store.SaveReport calls scheduled every20ms for at least three seconds. Check
incident/delivery/attempt and aggregate pages, exact aggregate context, audit,
queue diagnostics and idle workers. Completed history must not become pending,
be claimed again or mutate. Report attempts, successful writes and final
cancellation are separate; these numbers are not Agent throughput or ACK proof.

Hash every typed column of all10 seeded table subsets in primary-key order.
Reads, idle claims and concurrent reports must keep them unchanged. The eight
permanent incident/delivery/attempt/aggregate/event/audit subsets must also remain
unchanged through fresh ordinary/aggregate/system cycles. Do not falsely treat
bounded backup history as permanent: new backup results may prune old completed
tasks according to [the existing policy](SCHEDULED_BACKUPS.md), while an oldest
future retry remains byte-for-byte intact. Normal metric retention advanced400days
must not alter any of the retained subsets. A consistent SQLite backup/reopen
must restore their exact hashes and the protected retry. Integrity and foreign-key
checks must pass. A new post-restore cycle must refer to its new work; the original
protected reminder must subsequently retry and complete once, not be lost or
claimed after completion.

Times are Store-call nearest-rank p50/p95/max with sample counts. Database,
allocated SQLite pages and end-of-level WAL size are separate; end WAL is not a
peak. These measurements do not replace the unchanged overview/history/RSS SLO,
continuous24hours, encrypted actual-application restore, or real-provider tests.

## Run locally

Set an absolute, existing private evidence directory. Result creation is
exclusive. Failed synthetic fixture directories remain there for inspection;
successful owned temporary databases are removed after all checks.

```powershell
$env:MYPROBE_TEST_NOTIFICATION_HISTORY_CAPACITY='1'
$env:MYPROBE_TEST_NOTIFICATION_HISTORY_OUTPUT='<absolute existing directory>'
go test ./internal/store -run '^TestNotificationPermanentHistoryCapacity$' -count=1 -timeout=15m -v
```

MYPROBE_TEST_NOTIFICATION_HISTORY_MAX may be1000 or10000 for a smaller local
development check. Omit it for all three levels; a smaller run is not the full
100000-record result. The test is skipped in the ordinary baseline.

The Linux CI helper builds the exact clean checkout, checks all source bytes
against Git blobs and records the executable hash. A private-network systemd
unit limits the complete Store process to2CPU/2GiB/no swap and CPUs0–1. It
records the live executable/namespace/limits, resource observations, terminal
state, and unchanged source/program hashes. The host's actual memory is recorded;
an aggregate cgroup is not a physical2GiB VM. Only screened JSON is uploaded;
ephemeral CI databases and logs are excluded. A failed CI fixture lasts only as
long as that runner, unlike the explicit local output directory.

## Local result and remaining native check

The complete Windows run passed in67.62seconds (Go1.26.5, CGO=0, unrestricted
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
documented1000-task pruning explained the failing assertion. Those failed
fixtures remain in the private local validation output, and are not product bugs
or successful large-run evidence.

The exact-source bounded Linux CI result is still required. The native source,
executable, resource and result proof must be checked before recording that pass.
No application/schema/retention policy change is included, and the frozen
application50-node/24-hour run remains independent.
