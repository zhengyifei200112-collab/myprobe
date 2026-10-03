# Node detail routes and bounded history (DETAIL-01)

Status: in development on a branch based on current main. No release claim.

## Scope

Add a public `/nodes/:id` page and authenticated `/admin/nodes/:id` entry while
retaining the quick history dialog. The public page uses only server-filtered
public node data. Hidden and missing nodes both produce a not-found view; cached
dashboard data must not resurrect an inaccessible node. Share pages continue to
use their scoped APIs, never the administrator detail path.

The page separates overview, resource percentages, network rates and latency.
Preset and custom absolute time bounds survive reload via URL parameters. History
APIs validate both bounds, enforce maximum one-year ranges, choose bucket sizes,
and return explicit start/end/bucket metadata. Samples beyond the selected end
must not affect output. Gaps are displayed as gaps rather than smooth continuity.

Event timeline integration belongs to DETAIL-02 and depends on incident delivery
PR #50. This independent work does not copy private incident/audit data into public
responses or claim maintenance and event timeline features before implementation.

## Acceptance

- Public detail/hidden/missing and administrator unauthenticated/authenticated
  routes have consistent visibility and explicit empty/error behavior.
- A hard reload preserves node and selected time bounds; browser navigation works.
- Existing preset callers retain compatibility. Invalid/reversed/oversized bounds
  fail without expensive unbounded queries; raw and rollup edge cases are tested.
- Resource percentages and network rates have separate plots, shared time cursors,
  zoom/reset controls, stable units and missing-data breaks.
- Responses exclude tokens, raw host identity/IP, private targets and audit data.
- Check keyboard operation, 360/768/1440 light/dark rendering and stale requests;
  regenerate embedded assets and run repository checks before review readiness.

No schema or Agent protocol change is intended. Existing public list serialization
is the privacy authority for single-node detail. Any necessary history query
extensions must preserve that boundary and existing chart-share authorization.
