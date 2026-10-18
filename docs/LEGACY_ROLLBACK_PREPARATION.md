# Preparing a legacy rollback snapshot

`rollback-prepare` is an offline operator command for a **closed, consistent
schema-013 backup**. It creates a separate SQLite file with three additive history
lookup indexes. It does not start a Server, alter the original backup, apply newer
application migrations, rewrite rows, or change the old retention policy.

An isolated rehearsal found that the deployed old Server exceeded its unchanged
900-second readiness budget when started against an untouched legacy backup. A
fresh copy with these three indexes became ready in 5.49 seconds under the same
CPU, memory, I/O priority and readiness budget. That is evidence for this specific
prepared-copy path; it does not make the untouched-backup rollback pass, establish
a performance guarantee, or identify every cause of the earlier timeout. The
Python diagnostic query plans were produced by SQLite 3.40.1 and did not show an
expression seek; they are not evidence of the old binary's SQLite 3.50.4 plan.

## Operator procedure

1. Retain the original backup, its trusted SHA-256 record, old Server binary/image,
   and original encryption/configuration secrets in private storage. A database
   backup alone cannot decrypt stored credentials. Do not run this command against
   a live production database or a database that still needs WAL/journal recovery.
2. Build the command for the target host:

   ```sh
   go build -o rollback-prepare ./cmd/rollback-prepare
   ```

3. Use an operator-owned private directory with enough space for the complete
   backup, indexes and SQLite temporary work. Use a new destination on a filesystem
   supporting exclusive hard links. Existing destinations are never replaced.

   ```sh
   umask 077
   export TMPDIR=/private/rollback-work/tmp
   ./rollback-prepare \
     --source /private/backups/legacy.db \
     --destination /private/rollback-work/prepared.db \
     --expected-sha256 "${TRUSTED_BACKUP_SHA256}" \
     > /private/rollback-work/preparation.json
   ```

   Create the private temporary directory before running the command. The default
   command deadline is 15 minutes. A nonzero exit is a failure: inspect privately
   and retain the original backup; do not continue to application startup merely
   because a temporary file exists. Avoid redirecting over an existing receipt.

4. Keep the successful JSON receipt alongside the prepared file. It binds the
   original and prepared file hashes, preparation migration hash, whole-table
   content digest, table/row/report counts, sizes, schema version and index names.
   Independently verify the prepared file SHA-256 before using it.
5. Rehearse the **actual old application** on another copy of the prepared file in
   an isolated network with outbound checks/notifications disabled. Use the real
   startup policy and agreed resource/readiness limits. Verify stored login,
   original node membership and expected retention effects. Startup of the old
   application may legitimately expire reports; the preparation step must not.
   Retain failures and exact source/binary identities. Switch production only
   through the repository owner's approved release/rollback procedure.

The command has no credentials or production addresses in its arguments or output.
The prepared database still contains sensitive data and needs the same protection
as the source. The receipt contains content/file hashes and aggregate counts;
store it privately unless it has been screened for publication.

## Guarantees and limits

- The source is opened as an ordinary read-only file, never through SQLite. The
  complete copied bytes must match the caller's trusted SHA-256. Nonregular files,
  source symlinks and nonempty adjacent WAL/journal files are rejected.
- Only a private temporary copy is opened through SQLite. Its latest application
  migration receipt must be `013_github_oauth.sql`; other versions are rejected.
- The forward-only preparation migration
  `internal/rollbackprep/migrations/001_history_epoch_lookup.sql` is a **separate
  preparation track**, using the index definitions from application migration 041.
  It adds no application migration receipt. Schema-013 tables and all stored rows,
  row IDs, credentials, audit data and existing application receipts remain equal
  according to a typed, ordered whole-table digest before and after preparation.
- Existing indexes with the preparation names must have the expected definitions.
  A conflicting definition aborts preparation. Re-preparing an already prepared
  closed copy into a new destination is supported.
- SQLite integrity checks, content comparison, checkpoint/close and file hash
  finish before exclusive publication. The prepared file is flushed first. The
  command never replaces an existing destination. Keep the receipt and verify the
  file after an interruption or storage failure; publication is not a substitute
  for independently durable backup storage.
- This is not a down migration from the current schema, a general backup repair
  tool, an HTTP restore endpoint, or approval to release. It does not preserve
  writes made after the original restore point. Keep the untouched backup and the
  failed untouched-schema rehearsal evidence separately.

## Validation

`go test ./internal/rollbackprep -count=1` creates a synthetic database using all
released migrations 001–013. It checks report bytes, credential hashes, audit data,
binary values, migration receipts, source immutability, prepared-file hashes,
re-preparation and refusal to publish on a wrong hash, existing destination,
uncheckpointed WAL, unsupported version, conflicting index or cancellation.
The ordinary repository Go test job covers this command's package on Linux; the
local Windows test also exercises exclusive publication on NTFS.

The native Python diagnostic motivates this implementation. It must not be
reported as execution of this Go command; command-specific native rehearsal is a
separate acceptance item until its source-bound evidence is recorded.
