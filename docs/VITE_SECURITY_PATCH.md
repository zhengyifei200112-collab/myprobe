# Pinned Vite security patch

The current frontend build uses Vite8.1.6, the patched release on the existing
8.1 line. The21October2026 CI audit rejected8.1.5 after the upstream
launch-editor advisories were reflected in the audit database:

- [Windows percent escaping](https://github.com/advisories/GHSA-698v-4r3x-3vcv)
- [Editor flag interpretation](https://github.com/advisories/GHSA-r825-w8mm-5pxw)
- [Windows existence-check bypass](https://github.com/advisories/GHSA-587h-7wmj-h5pm)

Each lists8.1.6 as patched for the Vite8.1 line. The patch is pinned explicitly;
the normal dependency audit gate remains enabled. The change does not use a
forced upgrade, audit suppression or dependency override.

These advisories concern development editor launching. The Go Server serves
embedded production assets and does not run the Vite development server or its
editor-launch endpoint. This does not make a vulnerable development dependency
acceptable: local developers may run Vite on Windows or WSL, and CI should remain
auditable. Do not infer that the production Go endpoint exposes this execution
path from the dependency audit alone.

Regenerate and inspect the embedded assets after the patch, run the existing
frontend tests/typecheck/build, ordinary Go baseline and exact-source CI. Report
the actual asset comparison rather than assuming identical generated output.
If generated assets change, current-source native/browser acceptance must use
the new bundle. Even if assets are identical, preserve source and binary
identities for the required five-to-twenty/24h sequence.

The earlier a884dc1 CI audit failure remains a failure with its original log and
audit JSON; the preceding78 five-node pass and twenty-node fixture rejection
retain their own source and scope. No native a884dc1 run started, and no partial
measurement may be relabeled or stitched into the new source's24h result.
