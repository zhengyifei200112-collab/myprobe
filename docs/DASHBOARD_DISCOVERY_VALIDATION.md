# Dashboard discovery validation (M1 / UX-01 and UX-02)

This integration reuses PR #46 on the current quantity-editor stack (#87),
preserving the original branch history, current network comparison entry and
exact price formatting. Other roadmap integration and acceptance work remains.

## Reproduce

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run build
go build -o /tmp/myprobe-discovery-server ./cmd/server
mkdir /tmp/myprobe-discovery-evidence
PLAYWRIGHT_MODULE=/absolute/path/to/playwright node scripts/run-form-units-qa.cjs \
  /tmp/myprobe-discovery-server /tmp/myprobe-discovery-evidence
```

Use a new empty evidence directory. The shared runner owns a disposable loopback
Server and in-memory synthetic administrator credentials. On Windows it uses
Edge by default; on Linux it uses installed Playwright Chromium. `BROWSER_CHANNEL`
can select another installed channel. Browser CI pins Playwright 1.62.1 and
retains the evidence with the exact source commit.

The runner first exercises actual backend quantity persistence, then retained
policy contracts, then `dashboard_browser_test.cjs`. Dashboard API/WebSocket
responses are 100 synthetic nodes. That fixture verifies the embedded frontend,
not backend ingestion, hidden-node filtering or capacity. Existing backend and
live network-comparison tests remain separate required gates.

## Coverage and evidence

- Ten frontend tests cover quantity math plus discovery intersections, seven-day
  expiry boundaries, waiting/interrupted/stale distinctions, missing values,
  stable metric ID ties, stored-name/default-order ties (including non-BMP names),
  target isolation, and URL precedence over local preferences.
- Dashboard browser checks cover search/tag/status intersections, zero results
  and reset, reload/target sorting, five-second reordering, pointer and keyboard
  holds, keyboard history entry/focus return, current network-comparison entry,
  and the global reconnect notice.
- Twelve dashboard screenshots cover 360/768/1440 px, light/dark, cards/table.
  Document overflow and visible mobile control text are asserted. Narrow tables
  scroll inside their labeled keyboard-focusable region with sticky node names.
- `discovery/result.json` records successful checks, browser version and images.
  The shared `run-manifest.json` binds its digest, source/script hashes, generated
  asset hashes and actual Server executable hash. A pre-commit run remains marked
  dirty; a separate Git-blob binding may establish equivalence to a later commit.
- The shared runner also retains eighteen quantity-form screenshots and six
  policy contract views. No real notification is sent by these fixtures.

## Compatibility and remaining acceptance

No API, schema, protocol or public fields are added. All new display decisions use
existing public responses. Public attention thresholds are fixed display rules;
they do not read administrator alert policies. Stale/interrupted samples are
excluded from live-rate totals and table resource/latency values; cumulative
traffic remains visible. A disconnected dashboard reports its transport state
separately from node report state.

Frontend assets must be rebuilt from LF sources, retaining both npm test globs
and existing notification-link/time-zone build checks. The Windows HostRoot test
fix from #86 is included, so the full local Go baseline is required to pass.
This UI integration is not included in the independent Oracle soak frozen at
6ad1137. Final integrated-source load, deployment, remaining batch/ordering/detail
features, owner review, merge and release are separate acceptance gates.
