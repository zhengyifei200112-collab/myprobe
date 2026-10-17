# Closed-backup upgrade rehearsal

This operator opt-in checks forward migrations against a copy of a consistent
closed SQLite backup, without starting the Server, Agent or notification workers.
It complements encrypted archive portability; it is not an application failover
or downgrade procedure. Never give it a live Server database.

`TestOfflineUpgradeRehearsal` skips by default. Build a native Store test executable
from a clean frozen source, record the source archive/executable SHA-256 separately,
and run it on the intended operating system/architecture. The declared commit in
the result alone does not prove the binary's source. No compiler is required on
the destination machine and no production data should be downloaded to a developer
workspace or uploaded to Git/CI.

## Input and owned output

Prepare a consistent SQLite online backup, close it, run its integrity/foreign-key
checks and record its SHA-256. Keep it and the original key/runtime configuration
in private storage. This helper requires the hash and rejects a symlink input,
nonempty WAL/rollback journals, nonprivate Unix paths and populated output. Source
reads use `mode=ro&immutable=1`; only two exclusive new output files are migrated
or restored. Input and restored file hashes are checked again at the end.

Create a new empty private output directory and pass paths/hashes through
environment variables, never passwords/tokens. Example with placeholders:

```sh
MYPROBE_TEST_OFFLINE_UPGRADE=1 \
MYPROBE_TEST_SOURCE_COMMIT='<full frozen source commit>' \
MYPROBE_TEST_UPGRADE_BACKUP='<absolute closed backup.db>' \
MYPROBE_TEST_UPGRADE_SHA256='<verified backup sha256>' \
MYPROBE_TEST_UPGRADE_OUTPUT='<absolute empty private directory>' \
/absolute/store.test -test.run='^TestOfflineUpgradeRehearsal$' \
  -test.count=1 -test.timeout=6m -test.v
```

The operation has a five-minute context deadline, including bounded reads/copies.
Apply suitable CPU/memory/I/O controls when running on a machine that also hosts
production workloads. It does not change global settings, stop a service or
restart the host. On Windows, protect both input and output with ACLs; Go file
modes do not substitute for Windows access controls.

## Checks and limits

The helper retains original migration receipts and column lists privately, hashes
ordered rows of users, sessions, nodes, Agent token records, raw metric samples
and audit logs, then migrates `upgraded.db` through the current Store. It checks
full integrity/foreign keys, exact preservation of those original columns and
counts, and idempotent reopen. New columns may acquire migration-defined defaults;
the result does not assume the entire schema remains identical.

A separate `old-schema-restore.db` is copied from the original backup and opened
read-only, never by the new Store. Its receipts, integrity and complete file hash
must match the original. The source backup must remain byte-identical. No existing
destination is overwritten, and failure leaves private copies for inspection
without writing a success result or cleaning arbitrary paths.

Only after all checks pass is a new private `result.json` written. It contains
source/executable/input hashes, platform, migration names, counts and completed
check names. It excludes row data, row digests, personal identifiers, credentials
and the key. Keep the full output directory private: its databases contain the
real restored data even though the result is screened.

This verifies Store migrations and a restore copy. It does not start either old
or new application, log in, prove an old Agent's live compatibility, upload an
archive through HTTP, deliver a Provider message, switch traffic, or prove a full
release rollback. Actual deployment still needs its matching key/configuration,
old executable/image, application health, authentication and Agent checks. Restore
the old database plus matching runtime as a unit; do not put an old executable
against the migrated database.

The ordinary `TestOfflineUpgradeRehearsalPreservesLegacyAndRejectsUnsafeInputs`
uses a generated migration-013 database, verifies forward migration/preservation,
rejects wrong hashes/live journals/reused output, and checks that the source stays
unchanged. Default CI never receives an operator backup path or real data.
