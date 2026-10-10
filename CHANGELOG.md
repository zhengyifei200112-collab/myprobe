# Changelog

All notable changes to MyProbe are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases
use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Policy writes revalidate template references inside their transaction, rejecting
  imports when a template disappears after service validation without partial writes.

- Delivery retry deadlines round up to SQLite millisecond precision, preventing
  sub-millisecond truncation from allowing a retry before its Retry-After deadline.

- Independent in-memory Stores now use separate SQLite shared-cache names, while
  pooled connections within each Store still share its database. This prevents
  state and lock coupling between otherwise isolated test/service instances.

- Policy preparation status now uses the same alert service as the running
  evaluator, preventing the management UI from remaining pending after application.

### Added

- Authenticated policy-import preview API with explicit destination mappings,
  strict bounded input and transactional dry run. Submission binds the source
  content to the preview digest, revalidates destinations, and returns original
  results for committed retries. The import UI remains in development.

- Internal atomic policy-import replay ledger (migration 023), preserving original
  results across concurrent retries and restarts. The full transfer workflow remains unfinished.

- Authenticated scoped-policy JSON export with versioned definitions and source
  references, excluding notification credentials and runtime history. Import and
  the management-page transfer workflow are still in development.

- Scoped-rule synchronization reuses one validated policy snapshot across nodes,
  reducing repeated selection work without retaining stale cross-cycle caches.

- Policy-managed rule cards now identify their owner and open its current policy
  editor directly, instead of offering legacy edits/deletes that would conflict.

- Scoped policies now drive alert evaluation: each tick atomically maps legacy
  rules and applies node/tag selection before incident reconciliation. The policy
  UI reports preparation state and last successful application. Mapped rules are
  edited through policies; preparation failure prevents initial worker startup.

- Managed alert observations and notification claims/pre-send checks validate the
  current policy and node tags, rejecting stale execution rules before the next
  synchronization while preserving notifications across cosmetic policy edits.

- Atomic scoped-policy preparation combines legacy mapping and execution-rule
  synchronization, rolling back both if materialization fails. Runtime activation
  remains pending.

- Legacy rule edits/deletes reject policy-managed execution rules with an actionable
  conflict, preventing divergence from the owning scoped policy.

- Internal scoped-policy execution-rule synchronization with stable identities,
  dynamic tag selection and transactional removal of bound rules. Production
  policy activation remains pending.

- Internal legacy-rule policy mapping preparation (migration 022), preserving rule,
  incident and queued-delivery identities. Automatic runtime cutover remains pending.

- Notification-center scoped-policy configuration and per-node inheritance preview,
  with conflict-preserving edits and a clear pending-runtime notice.

- Notification channel deletion reports an actionable conflict when scoped
  policies still reference the channel, preserving those configurations.

- Administrator scoped-policy CRUD and effective-policy preview APIs, with CSRF,
  bounded input, revision conflicts and explicit pending-runtime status.

- Internal scoped alert-policy storage and effective-policy resolution with
  revision checks and transactional conflict detection (migration 021). Runtime
  evaluation, administration and legacy-rule migration remain in development.
- In-development migration 015 introduces separate incident, notification delivery
  and attempt storage, with compatibility import of existing active/pending states.
  This foundation must ship together with the new evaluation and delivery engine;
  it is not yet a completed incident-center release.


- A server-persisted Settings Center for site identity, dashboard copy, browser title,
  logo/favicon, footer links, Light/Dark/System themes, five accent presets, and
  independent public, admin, and login background presentation.
- A public read-only settings endpoint so branding and appearance can load before
  authentication without exposing administrator-only data.
- A unified Vue design system with semantic light/dark/system-ready tokens and
  accessible primitives for controls, surfaces, overlays, navigation, and feedback.
- Node creation and Agent token rotation now show a ready-to-copy Linux one-click
  Agent installation command using the current Server URL.
- Site settings now support a custom reverse-proxy-safe Agent connection address,
  public dashboard title/description, and sanitized header/footer content.
- Ping and TCPing targets can now be assigned directly to selected nodes from either
  the target editor or the node editor.
- A unified notification workspace supports encrypted Webhook, Telegram, Discord, and
  STARTTLS SMTP channels, test delivery, alert rules, reusable templates, recovery
  notifications, cooldowns, deduplication, and delivery history.
- Optional GitHub OAuth authentication uses single-use expiring state values and a
  case-insensitive username allowlist while retaining password login as a safe fallback.
- Reviewed Nginx and Cloudflare configuration provides the production HTTPS domain,
  WebSocket upgrades, real-client-IP handling, HSTS, and certificate renewal hooks.

### Changed

- Existing site settings JSON upgrades in place with complete defaults and legacy
  dashboard-copy compatibility; no database table migration is required.
- Redesigned the administration shell and node-management workspace with a calmer
  responsive layout, focused node metadata, grouped settings navigation, an add-node
  sheet, read-only configuration summaries, compact action menus, and accessible
  confirmations for destructive node operations.
- Redesigned the public dashboard around four focused summary cards and adaptive
  one/two/three-column node cards, with an optional four-column compact ultra-wide
  layout, prioritizing health, resources, network rate, and clear offline reporting
  while preserving detailed traffic, latency, and history views.
- Removed the standalone target-group screen from the administration UI; existing
  group assignments are migrated forward into direct node-target assignments.
- Unified the public dashboard's Apple-inspired information hierarchy across compact
  and detailed node-card modes, typography, badges, traffic, resources, latency, and
  responsive layouts.
- Increased dashboard typography and numeric hierarchy, tightened the node-toolbar
  spacing, and replaced character glyphs with a consistent responsive SVG icon set.
- Replaced platform-dependent flag emoji with bundled country flag artwork.
- Standardized percentages, byte totals, transfer rates, latency, temperatures, load,
  process counts, uptime, and chart tooltips across the dashboard and history views.
- Polished public and administration layouts at phone, tablet, laptop, desktop, and
  ultra-wide breakpoints, including safe-area handling and reduced-motion behavior.

### Fixed

- Unified notification and GitHub OAuth form controls with the larger design-system
  sizing, rounded surfaces, spacing, hover states, and accessible focus treatment.
- Reworked long-range history aggregation to avoid a correlated rollup scan that could
  leave node history charts waiting indefinitely on larger SQLite databases.
- Aligned public node facts, resource usage, network rates, and hardware capacity to a
  shared three-column grid, with responsive one/two/three-column node cards and an
  optional four-column compact layout on ultra-wide screens.

## [0.2.1] - 2026-07-22

### Changed

- Fresh Server installations and the default Compose configuration now expose
  `http://SERVER_IP:25775` directly, while an explicit reverse-proxy mode retains
  loopback binding and HTTPS-only session cookies.

## [0.2.0] - 2026-07-22

### Added

- A dedicated Agent container image and Linux host-monitoring Compose template now
  expose host metrics through read-only mounts while retaining outbound-only Agent
  communication.

### Fixed

- Agent containers no longer inherit the Server `/healthz` check, include the `ping`
  utility required by Ping tasks, and report logical host mount paths instead of the
  container bind-mount prefix.

## [0.1.1] - 2026-07-22

### Fixed

- Docker release builds now copy Vite output from the configured embedded asset path,
  with a pull-request container build preventing regressions before a release tag.

## [0.1.0] - 2026-07-22

### Added

- Multi-path deployment with a checksum-verifying Linux one-click installer for Server
  and Agent, lifecycle commands, published GHCR images, binary instructions, and
  Chinese installation documentation.
- Complete Simplified Chinese README covering features, deployment, configuration,
  security, development, and maintenance workflows.
- Repository governance baseline for GitHub Flow, Conventional Commits, code ownership,
  structured issues and pull requests, dependency updates, security reporting, and
  repeatable releases.
- Original Go Server and Agent, Vue dashboard, SQLite persistence, realtime monitoring,
  historical rollups, administration, alerts, sharing, maintenance, and deployment
  packaging.

[Unreleased]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/zhengyifei200112-collab/myprobe/releases/tag/v0.1.0

### Incident queue development (unreleased)

- Decouple incident evaluation from notification delivery with transactional jobs,
  four leased workers, bounded retries, safe error classifications and Retry-After.
- Preserve incident tracking when channel decryption is unavailable. This draft
  work still requires freshness validation and administrator workflow completion.

- Fix notification center form save buttons to submit, and make new-rule recovery
  windows follow trigger duration unless explicitly overridden.
