# Node detail routes, history and timeline (DETAIL-01 / DETAIL-02)

Status: integrated on the current #90 stack, preserving #51's original history.
Current-head CI and review are required before delivery. No release claim.

## Node event timeline (2026-10-17)

The current branch adds DETAIL-02 after the #91 foundation described below.
See [the timeline contract](NODE_TIMELINE.md) for sources, pagination, privacy,
observation limits, migration 047 and rollback. The earlier statements about no
timeline or no migration describe the independent DETAIL-01 foundation only.

## Current integration validation (2026-10-17)

The current implementation retains the optimized one-node SQL predicate and
migration 041's indexed nanosecond keys. Both raw window bounds are direct index
predicates; an EXPLAIN regression requires the existing node/time index with both
bounds, alongside the existing no-correlated-retention-scan requirement. Public
and administrator projections share masking while keeping administrator metadata
authenticated. Billing queries retain their inclusive endpoint semantics.

The administrator login now keeps absolute node-history selectors separate from
notification selectors. Password and local OAuth continuation tests verify that a
pending notification takes priority over a stale node-detail return destination.
Both DataZoom and MarkArea components remain registered so the current network
comparison and new history charts coexist.

Windows validation passes thirteen frontend boundaries, notification/time-zone
contracts, complete Go tests/vet/command build, and four independent disposable
Server profiles. Detail checks include forty actual Agent reports, public hiding,
private history, session revocation/relogin and six real administrator views plus
six synthetic public views. The same integration retains six ordering, twelve
batch, eighteen quantity, six policy and twelve discovery views, for sixty-six
screenshots in total. No real Provider message or external GitHub authorization
is part of this local validation.

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run build
go build -o /tmp/myprobe-details-server ./cmd/server
mkdir /tmp/myprobe-details-evidence
PLAYWRIGHT_MODULE=/absolute/path/to/playwright node scripts/run-form-units-qa.cjs \
  /tmp/myprobe-details-server /tmp/myprobe-details-evidence node-details
```

Use a new empty directory. The shared runner owns only a loopback Server, random
in-memory credentials and its database. Windows defaults to Edge, Linux to installed
Playwright Chromium. `live-result.json` and `ui-result.json` distinguish actual API
acceptance from synthetic rendering; `result.json` binds both digests, and the
manifest binds source/scripts, generated assets, actual executable and results.
An earlier worktree run keeps its dirty marker; Git-blob equivalence is recorded
separately after committing. The portable collector fixture is included, so the
old Windows exception below is historical and does not excuse a current failure.

No new migration or Agent protocol change is introduced. Exact-head Linux/race,
capacity and Chromium CI remain required. The running Oracle soak is frozen at
6ad1137 and cannot be relabeled as this new integration's capacity/24h proof.
Incident timelines remain DETAIL-02 and require additional implementation against
the current incident/service model; source ancestry alone does not complete them.

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

## Implemented foundation

`GET /api/v1/public/nodes/:nodeID` returns one existing public node projection and
server_time with no-store caching. List and single-node reads share the same
serialization and privacy filtering; the database predicate selects only the
requested visible node. Hidden/missing IDs share the same 404 response. API tests
cover valid detail, masked documentation IP, token exclusion and visibility changes.
Store and HTTP API package tests pass. Both public and authenticated administrator
detail pages are implemented.

The time-window parser now validates preset or paired absolute RFC3339 bounds,
rejects duplicate/mixed selectors, reversed/future/over-one-year windows, normalizes
time zones, and chooses existing resolutions below 2,000 points per series.
Both public and scoped share history routes use this parser and bounded metric,
latency and traffic reads. Responses include start, end, bucket_seconds, interval
`[start,end)` and rollup_boundary_policy `complete_buckets_only`. Requests can use
`?range=1h` as before or paired URL-encoded `start` and `end` RFC3339 timestamps;
mixing these selectors is rejected. All series use the same captured server clock.

Raw samples use exact nanosecond boundaries, including optional timestamp fractions.
Only fully contained retention buckets contribute. A selected edge inside a stored
minute/five-minute bucket omits that bucket, since its original samples no longer
exist. Returned point timestamps label aligned buckets and may precede the requested
start; they do not assert that a sample exists at that instant. The UI must show the
boundary policy and actual absence of data, rather than drawing implied continuity.

Traffic shows cumulative observed counter deltas: the first in-range raw counter is
a baseline, not transferred bytes. Retained deltas are attributed to their retention
bucket and cannot reveal exact transfer times. Billing queries retain their existing
inclusive endpoints. No database migration or protocol change is needed.

Tests cover exact/fractional endpoints, same-second ordering, minute/five-minute
partial buckets, explicit public API metadata, rejected selectors and scoped share
authorization. TypeScript contracts and the fetch helper support absolute ranges
and cancellation while keeping preset callers compatible.

Validation on 2026-10-03: clean npm install, TypeScript/Vite build (embedded assets
regenerated), store and HTTP API tests, Go vet/build and diff checks passed.
Full Windows Go tests passed except the existing collector host-root path fixture
(`TestDiskUsagePathUsesHostRootForAbsoluteMounts`), tracked separately in PR #47.
The current API changes do not alter collector code. Linux CI must pass on the
current PR head before review readiness.

## Public page implementation and UI acceptance

The dashboard history dialog links to `/nodes/:id`. The page reads only the
single-node public API, never dashboard storage, and clears node/charts on failed
revalidation. It refreshes the overview every 30 seconds while visible, and
fetches history on navigation or explicit refresh. It does not label polling as
a realtime connection. Preset and absolute selections survive refresh and browser
back/forward; abort signals and generation checks prevent older responses from
replacing a newer selection. Invalid URLs show an error with a reset action.

Four charts separate percentages, network rates, latency and observed cumulative
traffic. ECharts groups link time cursors and zoom; sliders, inside zoom and
keyboard-accessible zoom/reset buttons are available. Missing output buckets
insert explicit nulls and isolated observations remain visible as dots. This is
a display of missing observations, not a claim about planned sample coverage.

`node --experimental-strip-types --test web/src/node-details/history-view.test.mjs`
checks URL contracts and gap insertion; CI runs it. The local browser script
`scripts/node-detail-ui-acceptance.cjs` uses a loopback server and synthetic API
fixtures, with PLAYWRIGHT_MODULE selecting the installed Playwright package.
PROBE_TEST_URL defaults to http://127.0.0.1:5173 and PROBE_SCREENSHOT_DIR optionally
collects screenshots. It covers 360/768/1440 light/dark, absolute-range refresh,
back navigation, delayed stale responses, keyboard retry and hidden-node removal.
These six combinations passed on 2026-10-03; screenshots were visually inspected.

## Administrator detail and live acceptance

`/admin/nodes/:id` uses the existing login form/session restoration and only the
authenticated `/api/v1/admin/nodes/:id` and `/history` endpoints. These endpoints
include hidden nodes, while public/share history still uses its existing visibility
and scope. Administrator snapshots retain private Agent metadata (such as version),
but IP masking and credential exclusion remain in force. A lightweight existence
query distinguishes an absent node from an existing node with no samples.

Password login and expired-session login retain the current path and time query.
On HTTP 401 the page clears private content and returns to the login form. GitHub
login stores a tab-scoped local detail destination; a successful return to /admin
can resume it. The destination validator rejects external paths and other admin
routes. No new server redirect parameter or OAuth protocol change is introduced.

`scripts/node-detail-live-acceptance.cjs` runs against a dedicated disposable
loopback Server using explicit PROBE_TEST_USERNAME/PROBE_TEST_PASSWORD credentials.
It creates one synthetic node, submits real Agent HTTP reports, exercises public
detail before/after hiding it, then tests administrator detail in all six viewport /
theme combinations and deletes only its created node. It verifies password login,
absolute-range reload, the node-list entry, local OAuth continuation and private
chart removal/relogin after actual session revocation. All six combinations passed
on 2026-10-03; screenshots were inspected. External GitHub authorization is not part
of this local test. Latency rendering uses the public synthetic fixture tests;
raw/retained private latency authorization is covered by Go tests.

The earlier 2026-10-03 branch recorded a collector-path exception. The current
integration includes that portable fixture fix and passes the full local baseline;
see the current validation section above. Exact-head CI and DETAIL-02 remain
separate gates.
