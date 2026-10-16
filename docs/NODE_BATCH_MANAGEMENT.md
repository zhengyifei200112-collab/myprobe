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

Forward migration `046_node_batches.sql` adds an internal config revision and triggers on
node configuration and direct target assignments. Heartbeat/report writes do not
invalidate previews. Legacy administrative/import writers pass through these same
triggers. The revision is not added to public node serialization. The earlier
feature branch used 014; its receipt, revisions, pending previews and completed
idempotency results are preserved when upgrading to 046. A transaction-scoped
preparation step adds the column on the current 045 schema or validates the known
legacy column. Unrecognized columns or missing legacy revisions refuse upgrade.
The historical 014 SQL is an immutable test fixture, not inserted into the current
migration directory. Released migrations 001–045 are unchanged.

Previews are persisted and owned by user ID, not by client-supplied node snapshots.
SQLite's writer reservation is acquired before reading and checking revisions.
Node updates, assignments, idempotency result and audit entry commit together. A
failure of any one step rolls back all steps. Audit records contain allowed field
changes, not tokens, notification secrets, passwords or actual infrastructure IPs.
Target writes apply only the assignment difference, preserving unchanged network
schedule epochs and task phases. A regression adds then removes a second target
and verifies the original epoch remains active with the same identity.

Preview creation prunes expired unapplied records and applied records older than
30 days. At most 100 pending previews per user are allowed. The retention period
defines the replay window; after deletion the client must create a new preview.
These operational records are not part of configuration export, but encrypted full
database backups retain them and the revisions.

Visibility changes send an internal refresh signal, replacing queued public metric
events so a full current public snapshot removes hidden nodes without a reload.
A refresh generation also rejects in-flight publications whose snapshot read started
before the visibility change. No new public fields or Agent protocol version is
introduced. Current optimized one-node publication is preserved behind that fence.
The earlier branch's unnegotiated configuration frames and optional v1 HTTP config
are superseded by the current v2 application-evidence contract and are not restored.

## Validation

Store tests cover concurrent retry, restart replay, ownership, exact expiry,
idempotency-key collisions, configuration and assignment conflicts, no-op results,
target replacement and rollback when auditing fails. API tests cover login/CSRF,
strict request parsing, public visibility and revision exclusion, refresh signaling,
and non-duplicated audit records. Hub tests exercise refresh with a full event queue.

Current Agent tests retain independent sampling, generation fences and negotiated
configuration. The gateway acknowledgement test runs against both single edits and
real batches, verifying new revision/nonce issuance, application acknowledgement,
stale/invalid acknowledgement rejection and persisted confirmation. Released v1 and
HTTP fallback behavior remain covered by existing compatibility tests. Store tests
ensure exact replays update liveness without duplicating history, while distinct
late samples remain in history without overwriting newer metrics or traffic
anchors. Conflicting payloads at one capture time refuse acceptance. Actual
ingress evidence remains transactional.
Concurrent duplicate reports insert one sample, and injected ingress failure rolls
back the duplicate's liveness write and retains the previous actual transport.

Upgrade tests cover current 045, chronological early-014-plus-later migrations,
pending previews and completed replay after upgrade, restart idempotence, rollback
on failed 046 receipt, unrecognized columns and missing legacy revision state.

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

The portable collector fixture from #86 is included; the full local Go suite,
vet/build and exact-head CI are required. The shared runner can reproduce this
fixture on a fresh loopback Server, using the existing binary and a new empty
output directory:

```sh
node scripts/run-form-units-qa.cjs /absolute/path/to/server /new-empty-evidence node-batches
```

The profile creates a separate database from quantity/discovery checks and records
source/script/assets/executable hashes plus the browser result digest. Linux uses
Playwright Chromium; Windows defaults to Edge. CI retains twelve batch images and
the result alongside the earlier form, policy and discovery evidence.

## Interval application

Use the current mutually negotiated v2 `config_apply.v1` extension. Its existing
connection-owned refresh loop observes batch writes; diagnostics distinguish desired,
dispatched and Agent-confirmed revisions. A saved batch is not proof of application.
HTTP fallback, offline and released v1 peers retain their existing compatibility and
diagnostic states; this integration sends no unnegotiated configuration frames.
Independent collection/reporting preserves measurement timestamps. Reporting sends
new sample generations rather than repeatedly transmitting one cached measurement;
when collection is faster, only the latest sample is retained. This does not promise
lossless delivery of all intermediate measurements.
Omitted interface and mount selections retain local Agent settings.

## Deployment

Back up before upgrading. Migration 046 is forward-only; rollback of the database
requires restoring the pre-upgrade backup, not editing or deleting the migration.
Rebuild embedded frontend assets when integrating with other pending frontend PRs.
