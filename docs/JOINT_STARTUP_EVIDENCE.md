# Server startup evidence for joint runtime tests

The original frozen long-run source `6ad1137` failed after its scheduled 12-hour
Server restart: the next process did not become healthy within 30 seconds. That
fixture discarded Server output, so its exact startup cause was not retained.
The original failure remains a failure, with no 24-hour acceptance result.

The Server now emits structured `startup phase` markers for `pending_restore`,
`database_open` and `history_retention`. A successful phase has `state=completed`
and `elapsed_ms`; its preceding `state=started` remains useful if the process
stalls or exits. These fields contain no database paths, credentials or settings.
`http_listen` has `state=starting`; this marker does not prove a bound listener.
The actual HTTP health response remains the readiness authority.

Startup ordering, SQLite transactions, background retention frequency, public
API, schema, Agent protocol and production timeouts have not changed. Default
background retention runs hourly. A failure at 12 hours does not itself prove a
12-hour retention backlog, a WAL recovery problem or any particular request cause.

## Private process capture

Capture is explicit and currently requires POSIX directory permissions. Create a
dedicated existing directory with mode 0700 and set this environment variable on
the test runner, not on the production Server:

```sh
install -d -m 700 /absolute/owned-run/private/process
export MYPROBE_TEST_LOAD_PROCESS_LOG_DIR=/absolute/owned-run/private/process
```

Each Server start creates an exclusive random `server-*.log` file with mode 0600.
Combined stdout/stderr is limited to its first 1 MiB per attempt; excess bytes and
write errors are counted without returning a broken stdout-pipe error to the
Server. Files remain open until the process has exited and its pipes have drained.
Each start has its own file, including the scheduled restart.

Raw output can contain generated fixture credentials or private paths. Keep the
entire directory private. Do not upload it as a GitHub artifact, include it in a
PR, or paste its contents into public logs. Existing CI uploads still copy only
their explicit aggregate result/PNG allowlists; private process logs are outside
those directories. Windows capture is rejected rather than claiming that POSIX
mode bits verify Windows ACLs. Ordinary Windows tests retain their existing output
behavior when capture is not selected.

The adjacent `.startup.json` is an immutable decision-time summary containing a
controlled class, elapsed time, relative log basename, captured/discarded byte
counts, write-error count, and an exit code only when completion was observed
before cleanup. It contains no raw output, absolute path, URL or environment.
For a healthy process, the raw log can continue to grow after that summary,
subject to the same cap. Treat its counts as a startup snapshot, not final size.

## Readiness and verification

The fixture keeps its 30-second readiness limit and one-second HTTP request
timeout. It recognizes an exited process immediately, restores the completion
for normal teardown, and records `process_exited`. Other outcomes are `ready`,
`readiness_deadline`, `parent_context_ended`, `invalid_health_endpoint` and
`exec_start_failed`. A forced cleanup exit is not mislabeled as an observed
early exit. Failure still fails the test; capture never turns it into acceptance.

`TestJointStartupReadinessClassification` covers health, deadline, parent
cancellation and an already exited process. `TestJointStartupPrivateCaptureBoundaries`
covers concurrent writes, cap, private permissions, relative/symlink/shared-output
refusal, screened summary and overwrite refusal. Native CI also runs these with
`-race`.

The isolated `run-joint-load.sh` first invokes `TestJointStartupFailureEvidence`
using its supplied real Server. A child fixture deliberately uses a directory as
its database filename. The parent requires prompt exit 1, private raw output and
an immutable `process_exited` summary; it parses the actual JSON log to require
an incomplete `database_open` phase and the database error. Expected child output
is never copied into the public test log. The normal joint load then runs with
all existing report, notification, timeout and resource gates.

This diagnoses future attempts. It does not retrospectively establish the cause
of the original failure or complete capacity/24-hour acceptance. The next frozen
candidate needs a fresh setup and continuous measurement with its own private
logs, identity, report reconciliation and terminal result. Do not combine that
measurement with the old failed run.
