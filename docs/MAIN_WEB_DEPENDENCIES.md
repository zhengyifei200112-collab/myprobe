# Frontend dependency patch for the current main

This change starts from main `86abe9a`, including the five integrated node-management
modules. It backports the dependency-only changes already developed on `abe9900`
and `633afa8`; it does not pull in the later application or capacity branches.

| Dependency | Previous lock | Patched lock |
| --- | --- | --- |
| Vue and matching `@vue/*` runtime/compiler packages | 3.5.40 | 3.5.44 |
| Vite | 8.1.5 | 8.1.6 |
| PostCSS | 8.5.20 | 8.5.31 |
| Nano ID | 3.3.16 | 3.3.20 |
| source-map-js | 1.2.1 | 1.2.2 |

Direct dependencies remain exactly pinned. Other lockfile updates follow the existing
compatible dependency ranges; there are no overrides or forced major upgrades.
The form and dashboard test commands remain enabled. Embedded assets must be
regenerated from this dependency set.

The 26 October 2026 baseline registry audit reports six affected packages: five high
and one moderate. These are dependency advisories, not a finding that the production
Go endpoints expose each upstream attack path. The production Server embeds static
assets, does not run Vite's development server, and does not use Vue SSR.
Vue still changes the shipped client runtime, so the four existing browser suites
must pass on the new bundle.

Upstream references:

- [Vue renderer attribute validation](https://github.com/advisories/GHSA-g2v6-rqmx-r4w6)
- [Nano ID generator termination](https://github.com/advisories/GHSA-2v37-7h3g-55p8)
- [PostCSS source-map handling](https://github.com/advisories/GHSA-fxqj-rqcc-2cmp)
- [Source-map section offsets](https://github.com/advisories/GHSA-68fv-2mgg-jv7q)
- [Vite editor escaping](https://github.com/advisories/GHSA-698v-4r3x-3vcv),
  [editor flags](https://github.com/advisories/GHSA-r825-w8mm-5pxw),
  [Windows existence checks](https://github.com/advisories/GHSA-587h-7wmj-h5pm),
  [custom middleware](https://github.com/advisories/GHSA-9jrq-w75r-8gcw), and
  [filesystem allow-list handling](https://github.com/advisories/GHSA-rq7h-c2jc-7f22)

CI now runs `npm --prefix web audit --audit-level=moderate` after the locked install.
Registry unavailability or a new moderate-or-higher advisory fails the job; it is
not suppressed or treated as a clean audit. Audit results are time-sensitive and
do not establish the absence of other vulnerabilities.

Validation includes the locked install, registry audit, frontend tests/typecheck/build,
reproducible embedded assets, complete Go baseline, and quantity/dashboard/batch/order
browser regressions in light and dark themes at 360, 768, and 1440 px.
The change adds no API, Agent protocol, or database migration. Deploying it requires
rebuilding/replacing the Server to obtain the new bundle; rollback can use the prior
Server binary without a schema restore. Exact-source CI and owner merge approval
remain required. No production deployment or capacity acceptance is implied.
