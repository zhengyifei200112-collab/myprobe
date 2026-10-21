# Self-use acceptance fixture and import receipt boundaries

The required profile is five-to-twenty strict short runs, then twenty nodes for
an uninterrupted24h measurement. The native78afb18 five-node short run passed;
its twenty-node attempt exited before dense seeding because the history fixture
still accepted only5/50/100 nodes. That original failure/log/small fixture remains
retained. It is not a twenty-node pass or a production Server failure. Its
twenty-node24h successor never started.

The dense fixture now also accepts20 nodes. Actual per-node history density,
thirty-day/year assertions, disk headroom, preparation budget, five rendered
pages, report/target frequency, recovery accounting and all strict SLOs are
unchanged. Fifty/hundred-node cases remain optional. The regression is the actual
same-source20-node workload, not an assertion that a number occurs in an allowlist.
Each new source-bound attempt gets a new owned directory and must preserve its
own result; no automatic retry, reused failed corpus or stitched measurement.

The two capped import receipt ledgers also have explicit full-history restore
checks in `internal/store/import_ledger_capacity_test.go`:

- `alert_policy_imports`: a real import,9,998 synthetic copies of its valid receipt,
  then a real10,000th receipt. A10,001st receipt fails with the established limit
  error without creating definitions. Original and last successful requests
  replay with their original IDs; changed content conflicts. A consistent
  snapshot is staged and applied to the live path after an actual post-snapshot
  deletion; reopening preserves both the full quota and original identities.
- `service_alert_imports`: the equivalent boundary uses actual service import
  previews/applications. After deleting the destination services/channel, stored
  results still replay without recreating those objects. A restored full
  consistent snapshot preserves the10,000 receipts, limit and conflicts.

Filler rows are copied completed receipts, not10,000 actual API calls, active
tasks, independent import definitions or a throughput benchmark. These are
existing hard limits, not permanent unlimited capacity or permission to silently
discard retry history. Node previews remain separately bounded by100 pending
per user/ten-minute validity and30-day applied-result retention. Maintenance and
incident action evidence retain their own declared boundaries.

This change modifies acceptance tests and documentation only. No production Go,
schema, protocol, public API, frontend sources or embedded assets change.
Ordinary tests and these boundary checks do not replace the complete native
five-to-twenty/24h results, independent steady-memory review, real provider/user
acceptance or owner merge/release approval.
