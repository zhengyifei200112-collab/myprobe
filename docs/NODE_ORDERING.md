# Accessible node ordering (M1 / UX-03)

Status: implemented on the feature branch; merge and release determine availability.

The node page provides an order dialog with named up/down buttons, keyboard
activation, position announcements, cancellation and one explicit save. Hidden
nodes remain part of the administrative order; their visibility is unchanged.

`POST /api/v1/admin/nodes/reorder` accepts an `expected` array of `{id, sort_order}`
in the observed order and `node_ids` containing the complete proposed permutation.
The request is authenticated, CSRF protected and strictly parsed. At most 1,000
nodes and a 256 KiB body are accepted. This is a complete-list operation, not a
partial reorder. A concurrent creation, deletion or ordering change returns 409.

The server reserves the SQLite writer before comparing state. Changed ordering
normalizes integer sort values to 0..N-1 and updates only ordering fields; audit
and writes commit together. The same request may be retried after response loss:
if the current order already exactly matches the normalized desired order, the
server returns success without another audit. Other stale requests return 409.
An unchanged permutation leaves existing integer values untouched.

Ordering with equal numeric values uses name then ID for a deterministic fallback.
Existing manual integer editing, configuration transfer and public field shapes
remain compatible. No schema or Agent protocol change is needed. Public APIs show
the saved order on their next fetch; a currently open public dashboard may need a
refresh to show the complete updated order before subsequent metrics arrive.

## Validation

Store tests cover duplicate/omitted IDs and size bounds, equal/negative sort values,
concurrent retry, reopen/retry, stale creation/deletion/order changes, unchanged
integer preservation, reports that do not invalidate the order, unrelated metadata
preservation and rollback on audit failure. API tests exercise real login, CSRF,
strict nested fields, trailing JSON, body limits, stale responses and public order.

`scripts/node_order_browser_test.cjs` uses an isolated real Server and database.
It verifies native keyboard button activation, Alt+arrow movement, boundary buttons,
focus following the moved node, cancel/no-op, persisted order, hidden-node metadata,
response-loss retry with a single audit, concurrent creation conflict, reload
recovery and Escape/focus restoration. Reload and failed saves explicitly return
focus inside the dialog after temporarily disabled controls lose focus.

Light/dark screenshots at 360/768/1440 px cover the dialog. The list scrolls while
footer controls remain reachable. The script waits for theme transitions before
capturing screenshots. Set `PLAYWRIGHT_MODULE` to a Playwright installation and
install Edge; use `MYPROBE_TEST_URL` (default loopback port 25777),
`MYPROBE_TEST_PASSWORD`, and optionally `MYPROBE_TEST_OUTPUT`. The server must use
an empty disposable database. Test data is synthetic; do not use production.

Local validation includes frontend clean install/build, Go tests/vet/build and
`git diff --check`. The existing Windows collector absolute-path fixture fails on
this main-based branch and is fixed separately by PR #47; Linux CI must pass the
full suite before this PR becomes ready. No unrelated fixture changes are bundled.

## Integration

This feature starts from main independently of M1 form, discovery and batch PRs.
When integrating them, retain both administration components and rebuild embedded
assets. Batch configuration revision triggers should observe actual sort-order
updates; the writer reservation only touches `updated_at` and does not invalidate
configuration previews by itself. No new migration is introduced here.
