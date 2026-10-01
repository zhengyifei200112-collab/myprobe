# Node batch management (M1 / OPS-01 and OPS-02)

Status: in development. This document describes the implemented transactional
configuration path. Do not mark this feature ready until browser checks and Agent
interval application have been verified as well as CI.

## User flow

In the node administration page, expand batch management. Search by name or tag,
select nodes across 20-item pages (maximum 100), select an operation, and request
a server preview. Review each node's before/after configuration including no-ops,
then confirm the entire batch. Changing inputs invalidates the local preview.

Supported operations are add/remove tags, visibility, collection/report intervals,
and add/remove/replace probe targets. Target assignment defaults to additive mode.
An empty replacement explicitly clears assignments. Prices, credentials, deletion
and expiry are excluded. Every mutation requires a fresh authenticated session and
CSRF protection. Unknown request fields are rejected recursively.

## API

- `POST /api/v1/admin/nodes/batch/preview`: node IDs and allowed operations. Returns
  an opaque owned preview ID, ten-minute expiry, configuration revisions, and each
  node's before/after state. Does not change any node.
- `POST /api/v1/admin/nodes/batch/apply`: `preview_id`, `idempotency_key` (16–128
  characters). Returns changed and unchanged IDs and the original application time.
- A changed/deleted node or removed required target rejects the whole transaction
  with HTTP 409 and conflicting node IDs. Expiry or key reuse also returns 409.
  Unknown or differently owned previews return 404. Invalid operations return 400.
- Retry uncertain requests using the same preview and key. Successful results
  persist across restarts and are replayable even after the preview expires.

## Database and atomicity

Migration `014_node_batches.sql` adds an internal config revision and triggers on
node configuration and direct target assignments. Heartbeat/report writes do not
invalidate previews. Legacy administrative/import writers pass through these same
triggers. The revision is not added to public node serialization.

Previews are persisted and owned by user ID, not by client-supplied node snapshots.
SQLite's writer reservation is acquired before reading and checking revisions.
Node updates, assignments, idempotency result and audit entry commit together. A
failure of any one step rolls back all steps. Audit records contain allowed field
changes, not tokens, notification secrets, passwords or actual infrastructure IPs.

Preview creation prunes expired unapplied records and applied records older than
30 days. At most 100 pending previews per user are allowed. The retention period
defines the replay window; after deletion the client must create a new preview.
These operational records are not part of configuration export, but encrypted full
database backups retain them and the revisions.

Visibility changes send an internal refresh signal, replacing queued public metric
events so a full current public snapshot removes hidden nodes without a reload.
No new public fields or Agent protocol version is introduced by this path.

## Validation

Store tests cover concurrent retry, restart replay, ownership, exact expiry,
idempotency-key collisions, configuration and assignment conflicts, no-op results,
target replacement and rollback when auditing fails. API tests cover login/CSRF,
strict request parsing, public visibility and revision exclusion, refresh signaling,
and non-duplicated audit records. Hub tests exercise refresh with a full event queue.

Remaining before readiness: browser/API end-to-end checks at required viewports and
themes, complete CI, and actual Agent interval consumption. The existing Agent
currently ignores welcome configuration and collects on the reporting cadence;
this must be repaired rather than describing a persisted interval as effective.

## Deployment

Back up before upgrading. Migration 014 is forward-only; rollback of the database
requires restoring the pre-upgrade backup, not editing or deleting the migration.
Rebuild embedded frontend assets when integrating with other pending frontend PRs.
