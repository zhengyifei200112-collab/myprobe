# Dashboard discovery validation (M1 / UX-01 and UX-02)

This change implements dashboard discovery and table comparison from the proposed
development roadmap (PR #44). Other M1 tasks and M2–M5 remain outstanding.

## Automated checks

- `npm --prefix web test`: 100-node intersection, expiry boundaries, stale/waiting
  distinctions, missing values, deterministic ties, target isolation and URL rules.
- `npm --prefix web run build`: TypeScript/Vue checks and embedded frontend build.
- `scripts/dashboard_browser_test.cjs`: build the frontend, run Vite preview on
  loopback port 4173, and execute the script with Playwright installed. Set
  `PLAYWRIGHT_MODULE` to reuse an existing module, and `BROWSER_CHANNEL` to choose
  a channel (default `msedge`). API/WebSocket results are synthetic fixtures;
  this browser test does not validate backend ingestion or load capacity.

The browser script covers 100 nodes, combined filters, empty results and reset,
reload/deep-link preferences, selected-target sorting, five-second reordering,
pointer and keyboard ordering holds, global disconnect behavior, and 12 screenshots
(360/768/1440 px, light/dark, cards/table). It checks document overflow and visible
mobile action text. Images go to `QA_OUTPUT_DIR` or the OS temporary directory.

## Contracts and remaining integration

No API, schema, protocol or public fields are added. All new display decisions use
existing public responses. Hidden nodes remain excluded by the server. Public
attention thresholds are fixed, described heuristics and do not consume admin rules.

PR #45 separately changes quantity forms and adds form unit tests. At integration,
combine the two npm test globs, retain both product-document sections and rebuild
embedded assets. Never choose one generated bundle over another without rebuilding.

On Windows the existing collector host-root test assumes Linux absolute paths.
Record its failure separately from this frontend change and require Linux CI to
pass. Browser and unit results do not replace the CI baseline or a capacity report.
