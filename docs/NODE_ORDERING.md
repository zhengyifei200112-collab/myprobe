# Accessible node ordering (M1 / UX-03)

Status: implementation in progress.

The node page will provide an order dialog with named up/down buttons, keyboard
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

Validation must cover duplicate/omitted IDs, bounds, equal and negative sort values,
concurrent changes/retries, unrelated metadata preservation, rollback on audit
failure, authentication/CSRF, mobile themes and focus after keyboard moves.
