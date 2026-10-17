# Synthetic startup backlog and retained failure fixtures

This opt-in test profile supports diagnosing startup under aged retained history.
It does not change production retention, startup ordering, clocks, the 30-second
readiness limit, or any report/notification/history acceptance assertion.

`MYPROBE_TEST_LOAD_BACKLOG_HOURS` is an integer from 0 to 8760 (one complete
365-day retention cycle), default 0. Nonzero values require the dense-year profile.
The generator shifts its UTC history anchor into the past, then fills real-density
15-second metric and 60-second three-target rows through the actual setup finish.
Minute/five-minute layers, counter origin and live counter continuation use that
same anchor. Requested age, actual anchor, row counts and setup timestamps are
recorded separately. UTC hour flooring and real setup time add to eligible age;
the option does not assert exactly N hours of expired rows or N hours of live run.

The generator's free-space guard reserves 500 MiB/node plus 1 MiB/backlog-hour/node
and 8 GiB spare. This is a conservative fixture admission guard, not a measured
storage estimate. Large node/age combinations may require a larger disposable
volume. Setup keeps its separate two-hour budget; measurement still uses real
elapsed time. A successful 90-second aged-data test is not a 24-hour soak.

## Preserve an owned failure corpus

Create separate existing POSIX directories with mode 0700:

```sh
install -d -m 700 /absolute/owned-run/private/fixtures
install -d -m 700 /absolute/owned-run/private/process
export MYPROBE_TEST_LOAD_PRIVATE_FIXTURE_DIR=/absolute/owned-run/private/fixtures
export MYPROBE_TEST_LOAD_PROCESS_LOG_DIR=/absolute/owned-run/private/process
export MYPROBE_TEST_LOAD_HISTORY=year MYPROBE_TEST_LOAD_BACKLOG_HOURS=12
export MYPROBE_TEST_LOAD_NODES=5 MYPROBE_TEST_LOAD_DURATION=90s
export MYPROBE_TEST_LOAD_RECONCILE=1 MYPROBE_TEST_LOAD_TIMEOUT=2h15m
# With the usual isolated/root runner and frozen supplied binaries:
sh scripts/run-joint-load.sh /absolute/agentclient.test /absolute/server
```

The fixture allocates a new private random child and always creates its own
`joint.db`; it never accepts an existing operator database. Before setup, its
exclusive `fixture-plan.json` records numeric inputs and the actual Server SHA.
After successful seeding, `history-fixture.json` records actual anchor/counts.
On test failure the owned child is retained. On success only that owned child is
removed after canonical-target and symlink revalidation; siblings remain intact.
With no private-fixture option the previous `t.TempDir` behavior is unchanged.

The retained directory can contain fixture credentials, browser sessions and test
host metadata even though metrics are synthetic. Keep it private, never commit
or upload the whole corpus, and inspect only after the unit/processes have exited.
Retained files last as long as the runner's filesystem; an ephemeral CI runner
does not provide post-destruction access. CI artifact allowlists remain unchanged.
Windows opt-in is rejected because POSIX permissions do not establish Windows ACLs.

The normal CI joint-load step now exercises five nodes with a configured 12-hour
data age and retained-failure support. Its independent online-backup profile keeps
the default zero-age scenario with five rendered browser pages. The basic backlog
step keeps its five Go WebSocket consumers; they are not rendered-browser proof.
Both retain their real Server/Agent, report/ACK, task, notification, history and
timeout gates. Native profiles can separately select real browsers. Pure tests also cover
input/time boundaries, retained bytes, sibling preservation and replaced-symlink
refusal, with native race checks.

This produces a new diagnosed scenario, not retrospective proof of the old
`6ad1137` 12-hour restart failure. That run discarded its Server output and cleaned
its test database. A new failure must be attributed to its own source, artifacts,
age, limits, stage logs and preserved corpus. A passing small aged-data profile
does not establish arbitrary-backlog, permanent-ledger-growth, target-hardware or
final-source 24-hour acceptance.
