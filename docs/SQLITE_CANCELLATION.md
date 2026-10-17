# SQLite cancellation and WAL readers

The Server now uses `modernc.org/sqlite` 1.42.2 instead of 1.39.1. Both embed
SQLite 3.50.4. There is no database migration, API, Agent protocol or retention
change. Rebuild the Server to obtain the driver fix; an existing process does not
gain it from updating sources or its database.

## Failure and fix

A request can be canceled after SQLite has produced its first result row but
before `QueryContext` returns that row to the caller. In 1.39.1, this path can
discard the newly created `Rows` without closing its SQLite statement. The caller
gets a cancellation error, has no rows to close, and returns its connection to the
pool. The orphaned statement can retain a read transaction on that idle connection.
Subsequent writes grow the WAL while checkpoints cannot advance past the reader.

Upstream 1.40.1 closes those unreturned rows. Version 1.42.2 additionally waits for
the cancellation interrupt to finish before returning the connection, preventing
an interrupt from spilling into its next query. The selected version includes
both lifecycle fixes while retaining the existing SQLite engine version. See the
[official changelog](https://github.com/modernc-org/sqlite/blob/master/CHANGELOG.md),
[row cleanup change](https://gitlab.com/cznic/sqlite/-/merge_requests/81), and
[interrupt cleanup change](https://gitlab.com/cznic/sqlite/-/merge_requests/86).

## Regression

`TestCanceledQueryReleasesWALReader` uses two production `Store.Open` handles on a
fresh database under `t.TempDir`, each limited to one connection. A test-only
scalar function cancels synchronously or schedules cancellation while the first
row's metadata is being constructed. The synchronous case guarantees pre-return
cancellation even on fast machines. The caller closes every returned `Rows` and
waits for its own cancellation callback. The test then verifies that the reader
connection is idle, its immediate next query succeeds without sleep/retry/reopen,
and another committed write can be fully backfilled by a passive checkpoint.

The fixture runs 400 cancellations with four scheduling offsets and requires at
least one cancellation error before `QueryContext` returns. It does not use an
operator database, repair an orphan by restarting the connection, or change
production timeouts or checkpoint settings.

```sh
go test ./internal/store -run '^TestCanceledQueryReleasesWALReader$' -count=1 -v
```

The default Go suite includes the regression. Native Linux CI also runs this
specific fixture with `-race`; a Windows `CGO_ENABLED=0` run is not a race check.

Before the update, the regression failed on Windows at iteration 0: the caller
received a cancellation error with no returned rows, the pool was idle, but only
520 of 521 committed frames could be backfilled. With 1.42.2 the same fixture
completed all 400 iterations: 104 canceled query errors, 296 explicitly closed
returned rows, every checkpoint complete and subsequent query usable. Counts
depend on scheduling; they are observations rather than fixed test expectations.

## Evidence limits and operations

The original frozen Linux long run separately showed a stalled WAL backfill and
old reader locks owned by its Server. The reproduction proves a defect in the
old dependency and the regression exercises the corrected path. It does not
identify the individual request that first held a reader in that long run, or
prove that an earlier isolated online-backup API failure had the same cause.

Keep the original long run's source, process and results unchanged. Do not open a
diagnostic SQL connection or issue a checkpoint against it to make the symptom
disappear. Its result remains evidence for its original source. A candidate with
this dependency update needs its own capacity and continuous 24-hour acceptance;
unit tests and a later successful CI run do not replace those requirements.

Existing deployments receive the fix on a normal approved Server upgrade.
Back up and retain the pre-upgrade database and executable according to the
application upgrade runbook. Do not run an older Server on a migrated database.
