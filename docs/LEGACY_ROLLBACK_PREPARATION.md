# Preparing a legacy rollback snapshot

`rollback-prepare` is an offline operator command for a closed, consistent
schema-013 backup. It creates a new copy with three additive history lookup
indexes. The source file is read as ordinary bytes, never opened in SQLite.
Only the temporary destination is opened in SQLite. No Server or Agent is started.

This prepares an old restore point for use with its matching old application. It
does not downgrade a newer database, recover an incomplete WAL snapshot, change
retention policy or preserve writes after that restore point.

Schema 013 is a specific original deployment baseline. The formal `v0.2.1` tag
contains migrations 001–010, not 001–013; this preparer deliberately rejects that
older input. Restore a v0.2.1 recovery point using its matching application and
consistent backup. Do not add migration receipts just to pass this command.

## Operator procedure

1. Keep the original consistent backup, its trusted SHA-256, matching old Server
   binary or image, and original encryption/configuration secrets in private
   storage. Do not use a live database or one that still needs WAL/journal
   recovery. Do not edit migration receipts to make a database appear compatible.
2. Obtain the preparer from a release containing this command, or build it from
   reviewed source:

   ```sh
   go build -trimpath -o rollback-prepare ./cmd/rollback-prepare
   ```

   The release workflow builds `myprobe-rollback-prepare-linux-amd64`,
   `myprobe-rollback-prepare-linux-arm64`, and
   `myprobe-rollback-prepare-windows-amd64.exe` and includes them in `SHA256SUMS`.
   A change on main does not itself publish these assets; check the selected
   release and its checksums. No automatic production rollback is performed.
3. Create an operator-owned private work directory and temporary directory. Keep
   enough free space for a complete copy, indexes and SQLite temporary work. The
   destination filesystem must support exclusive hard links. Use new file names
   for both the destination and receipt. For example, on Linux:

   ```sh
   umask 077
   mkdir -p /private/rollback-work/tmp
   export TMPDIR=/private/rollback-work/tmp
   set -C
   ./rollback-prepare \
     --source /private/backups/legacy.db \
     --destination /private/rollback-work/prepared.db \
     --expected-sha256 "${TRUSTED_BACKUP_SHA256}" \
     > /private/rollback-work/preparation.json
   ```

   Supply the complete SHA-256 from the trusted backup record. Use appropriate
   private filesystem permissions on Windows as well. The command deadline is
   15 minutes. A nonzero exit is a failure: keep the original backup and inspect
   privately. A temporary file or partial/empty receipt is not successful output.
4. Keep the successful JSON receipt with the prepared file. It records source and
   prepared SHA-256, preparation migration SHA-256, a typed whole-table content
   digest, row/report/table counts, file sizes, schema version and index names.
   Independently verify the prepared file hash before use. Do not overwrite the
   original backup with the prepared file.
5. Run the actual matching old application against another copy of the prepared
   file in an isolated network, with outbound checks/notifications disabled. Use
   its original retention policy and the agreed resource/startup limits. Check
   original login, necessary keys, node membership and expected data. The old
   application may expire history under its own policy; preparation must not.
   Record executable identities and stop outcomes. Keep the original backup and
   any failed rehearsal evidence. Switch production through the owner's approved
   release/rollback procedure.

The prepared database contains the same sensitive data as its source. Protect it
accordingly. The receipt has hashes and aggregate counts, not credentials or row
contents; retain it privately unless screened for publication.

## Validation and guarantees

- Require absolute source/destination paths, an ordinary source file, the trusted
  complete source SHA-256, and no nonempty source WAL/journal. Source symlinks and
  existing destinations are rejected. The caller must ensure the source is closed
  and stable in an operator-controlled directory; file checks do not stop another
  application from opening a live database.
- Require exactly the original schema-013 migration names 001–013, once each. Unknown,
  missing, duplicate, null/nontext and oversized receipts are rejected with a fixed
  error. This is a compatibility check, not semantic validation of arbitrarily
  edited tables or a migration ledger rewritten to conceal a newer schema.
- Execute only the separate preparation migration
  `internal/rollbackprep/migrations/001_history_epoch_lookup.sql`. It adds the
  three expression indexes for metric, latency and traffic rollup history. It
  adds no application migration receipt and changes no existing application
  migration. Conflicting index definitions are rejected; an already prepared
  closed copy may be prepared again into another new destination.
- Compare typed, ordered whole-table fingerprints before and after indexing,
  including table/trigger/view definitions, stored rows, row IDs, credential
  hashes, audit data and migration receipts. Complete integrity checks,
  checkpoint/close, file hashing and file flushing before exclusive publication.
  Publication does not replace independently durable backup storage: retain and
  verify the receipt and file after interruptions or storage failures.

Package tests construct synthetic databases from the original schema-013 migrations 001–013.
They verify exact source/content preservation, binary values and report bytes,
closed WAL input, exclusive publication and re-preparation; refusal covers wrong
hashes, live WAL, unsupported/incomplete/malformed migration histories, conflicting
indexes, cancellation and existing destinations. Command tests cover failures,
positional input and help without success receipts.

Historical external preparer or old-application measurements do not prove that a
new build of this command has passed an operational rehearsal. Bind each actual
rehearsal to its source, program hash, input hash, limits and original evidence.
