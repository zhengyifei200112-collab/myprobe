# Node batch management (M1 / OPS-01 and OPS-02)

Status: implemented on the feature branch; release availability depends on merge
and release. Transactional, Agent and browser checks are recorded below. Final PR
readiness additionally requires CI for the latest commit.

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
A refresh generation also rejects in-flight publications whose snapshot read started
before the visibility change. No new public fields or Agent protocol version is
introduced; HTTP acknowledgement configuration is an optional v1 field.

## Validation

Store tests cover concurrent retry, restart replay, ownership, exact expiry,
idempotency-key collisions, configuration and assignment conflicts, no-op results,
target replacement and rollback when auditing fails. API tests cover login/CSRF,
strict request parsing, public visibility and revision exclusion, refresh signaling,
and non-duplicated audit records. Hub tests exercise refresh with a full event queue.

Agent regression tests cover welcome configuration, dynamic timer interruption,
independent cached reporting, legacy HTTP responses, and HTTP configuration.
Gateway integration tests apply a real batch and verify configuration on an existing
WebSocket heartbeat and HTTP hello. Store tests ensure repeated/older samples update
liveness without duplicating history or overwriting newer measurements.

`scripts/node_batch_browser_test.cjs` passes against an isolated Server and real
SQLite database with 25 synthetic nodes. It exercises cross-page selection, preview
invalidation, no-ops, atomic conflict rejection, target add/remove/empty replacement,
interval persistence, live public visibility, and retry after a committed response
is deliberately dropped. Screenshots cover light/dark at 360/768/1440 px, including
the form and scrollable confirmation. The component uses shared design-system
controls, and the preview list can receive keyboard focus.

Run with `PLAYWRIGHT_MODULE` pointing to a Playwright installation, Edge installed,
and `MYPROBE_TEST_URL` pointing to a fresh loopback Server (default port 25776).
Set `MYPROBE_TEST_PASSWORD` to its disposable administrator password. The script
requires an empty database and creates synthetic records; do not run on production.
`MYPROBE_TEST_OUTPUT` optionally selects the screenshot directory.

Local Go regression tests, vet and build pass except for the existing Windows
absolute-path collector fixture, separately corrected in PR #47. Linux CI covers
the full suite. This exception is not a failure in Agent interval application.

## Interval application

Upgrade Agents for the `config.intervals.v1` capability. New Agents independently
collect and report, applying welcome settings and subsequent updates. A connected
Agent receives changes on its next report or 25-second heartbeat; HTTP fallback
receives them on its next hello/report response. The batch result confirms desired
configuration persistence, not an acknowledgement that every Agent has applied it.
Offline and older Agents may not apply the change until reconnection or upgrade.
When collection is slower than reporting, cached timestamps are retained and duplicate
history is suppressed. When collection is faster, only the latest sample is sent;
this does not promise lossless delivery of all intermediate measurements.
Omitted interface and mount selections retain local Agent settings.

## Deployment

Back up before upgrading. Migration 014 is forward-only; rollback of the database
requires restoring the pre-upgrade backup, not editing or deleting the migration.
Rebuild embedded frontend assets when integrating with other pending frontend PRs.
