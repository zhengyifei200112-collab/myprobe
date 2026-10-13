# MyProbe product specification

## Product principles

1. Provide a calm, information-first Apple-inspired monitoring experience without
   copying third-party source, assets, branding, or implementation details.
2. Keep the agent small, outbound-only, and safe by default.
3. Make every public metric explainable from a typed protocol field.
4. Preserve a one-command deployment and a single server binary release.
5. Treat offline state, traffic accounting, and notifications as correctness-critical.

## Feature parity matrix

Scoped alert-policy inheritance (ALT-02) is in development. Its internal resolver
and revisioned persistence support global, tag and explicit-node scopes, conflict
validation and snapshot-based effective-policy previews. Each runtime tick maps
legacy rules and applies policies atomically before incident evaluation. Management
APIs and UI report pending/ready/error preparation with the last successful time;
saving a policy is distinct from applying it or delivering a notification. Failed
startup preparation prevents notification workers from starting. Live browser,
full upgrade/restore, capacity and configuration-transfer checks remain release
gates. See [alert policy design](ALERT_POLICIES.md).

| Area | Requirement | Acceptance evidence | Status |
| --- | --- | --- | --- |
| Public overview | Current time, online/offline counts, aggregate traffic and rate | Responsive browser tests and live API data | Implemented |
| Public filtering | Tag/region pills with counts and horizontal mobile scrolling | UI test at 360/768/1440 px | Implemented |
| Node cards | Bundled country flag artwork, name, status, masked IP, OS, uptime, last update | Metadata persistence, privacy, API contract, and browser rendering tests | Implemented |
| Capacity | CPU model/cores, memory and disk totals and utilization | Agent integration test | Implemented |
| Network | Current up/down rate, lifetime and billing-cycle traffic | Counter-reset and interval tests | Implemented |
| Commercial metadata | Price, billing period, expiry date and remaining days | Exact, leap-day and past/future boundary tests | Implemented |
| Latency | Reusable Ping/TCPing targets, direct node assignment and current results | Scheduled task integration test | Implemented |
| Theme | Light, dark, and system preference | Browser theme tests | Implemented |
| Realtime | WebSocket updates, reconnect, cached last-known data | Disconnect/recovery test | Implemented |
| Charts | Ping, TCPing, upload, download, and total traffic history | 1h/12h/1d/3d/7d/30d queries | Implemented |
| Chart sharing | Group-scoped password protected chart views | Authentication and rate-limit tests | Implemented |
| Node administration | Register, edit, order, hide, delete, rotate token, generate one-click Agent install command | Admin API and browser clipboard verification | Implemented |
| Target administration | Ping/TCPing target CRUD and assignment from target/node editors | API and scheduler tests | Implemented |
| Notifications | Encrypted Webhook, Telegram, Discord, and SMTP channels with rules, templates, recovery delivery, cooldowns, and history | Mock receiver and alert-state tests | Implemented |
| Alerts | Offline/recovery, CPU, memory, disk, latency, bandwidth, cycle traffic and expiry | Deduplication/cooldown tests | Implemented |
| Custom display | Structured badges/links and sanitized advanced HTML | Sanitizer, persistence and CSP tests | Implemented |
| Site customization | Settings Center for Agent URL, identity, titles, sanitized header/footer, links, theme, accent, and page backgrounds | Store and browser rendering tests | Implemented |
| Configuration | Versioned merge import/export and encrypted database backup/restore | Round-trip, tamper, dry-run and recovery tests | Implemented |
| Authentication | Password login/logout/change plus allowlisted GitHub OAuth, CSRF, single-use state, persistent throttling, CAPTCHA, and password fallback | Security integration and restart tests | Implemented |
| Audit | Administrative action log and cursor-paginated viewer | API and management UI verification | Implemented |
| Retention | Transactional raw, one-minute, and five-minute history tiers | Migration, idempotence, cleanup and cross-tier query tests | Implemented |
| Deployment | One-command container startup and single-binary releases | Compose validation and tagged release workflow | Implemented |

## Public dashboard layout

- Header: product brand, persistent compact/detail display control, theme control, and
  admin entry.
- Overview: four cards on desktop, two columns at intermediate widths, and a single
  column on narrow mobile screens.
- Filter bar: `All`, dynamic tag groups, and `Other`.
- Node grid: one column through 899 px, two columns from 900–1099 px, and three
  columns from 1100 px. Compact mode may use four columns from 1800 px.
- Compact node cards show identity, commercial metadata, masked IP, and uptime. Detailed
  cards additionally show live/lifetime/cycle traffic, capacity utilization, latency,
  custom actions, operating system, last update, and history access.
- Node cards use soft elevated surfaces, restrained color, rounded corners,
  tabular numeric values, an integrated SVG icon language, and accessible warning
  colors. Overview totals remain visually dominant over their online/offline breakdown.
- Empty, loading, offline, reconnecting, and stale-data states are first-class UI states.

## Node metadata

- Stable UUID, display name, sort order, visibility, tags, and country override.
- Price in minor currency units, ISO currency code, billing period, expiration date.
- Traffic reset day, lifetime/cycle display choice, latency display mode.
- Structured badges and links; advanced sanitized HTML is opt-in.

## Metrics

- CPU usage, logical cores, model, architecture, and load averages.
- Memory and swap total/used values.
- Disk total/used values for configured mount points.
- Network interface, total counters, calculated rates, and counter reset handling.
- Uptime, operating system, kernel, process count, temperatures when supported.
- Public IP is derived from the authenticated agent connection rather than trusted from
  the payload; public responses expose only a server-masked value.

## Retention

- Latest state: in memory and persisted per node.
- Raw samples: seven days by default.
- One-minute rollups: 30 days.
- Five-minute rollups: one year.
- Retention and rollup jobs are configurable and transactional.
- Historical queries merge tiers without exposing raw long-range samples, including a
  one-year view backed by the five-minute tier.

## Explicit exclusions for v1

Remote shell, arbitrary command execution, and terminal proxying are intentionally
excluded from v1 to keep the agent attack surface small.

## Incident delivery development

The incident-outbox branch adds persisted pending/firing/resolved incidents,
independent recovery windows, stale observation handling, transactional delivery
jobs, leased workers, bounded retries and per-attempt diagnostics. Administrator
APIs and the notification center expose incident state separately from delivery
status. Protocol payloads remain compatible; SQLite migration 015 imports legacy
active state without mass resending successful notifications.

This work remains in development, not released. Evidence and remaining gates are
in [INCIDENT_DELIVERY.md](INCIDENT_DELIVERY.md). Scoped policy inheritance,
maintenance/acknowledgement/silence and aggregation remain later M2 tasks; this
foundation must not be presented as completion of the full roadmap.

## Incident acknowledgement development

The acknowledgement branch adds private first-actor/time/note records and
authenticated read/write APIs (migration 028). Identical committed requests replay
their original result; conflicting requests cannot overwrite the first actor.
Acknowledgement leaves condition state, notification jobs and repeat clocks
unchanged. Private incident lists return only author/time summaries. The management
UI provides private note/history and uncertain-result retry across reload or event
selection. Real receiver tests verify continued initial/repeat/recovery sends;
real browser tests cover both themes/three widths, conflicts and storage failures.
The feature is unreleased; dependency release integration remains pending. Silence
and aggregation remain separate work packages. See
[INCIDENT_ACKNOWLEDGEMENT.md](INCIDENT_ACKNOWLEDGEMENT.md).

## Maintenance development

The maintenance branch adds one-time windows, explicit/tag selections frozen on
creation, preview confirmation and authenticated create/cancel/list APIs. Metrics
and incidents continue during maintenance; enqueue, claim and pre-send checks
suppress notifications. Historical membership and cancellation remain readable
after node deletion. Store tests cover current-schema backup/restore, concurrent
requests and retries; API tests cover permission and strict input contracts.
The notification-center UI supports preview, frozen membership, history/status,
cancel and uncertain-result retry across reload. Incident details expose queried
current scope and actual historical window decisions, and canceled jobs expose
their recorded window links (migration 027), without historical cause backfill.
An already-started send may finish after job cancellation. Its latest attempt
retains the actual provider result while the job stays canceled, without retries
or repeat-clock changes. The delivery view explains a confirmed late success.
Real browser tests cover both
themes/three widths, and real local-channel workers validate suppression and a
continuing-fault summary. Released-version integration and additional release
evidence remain pending. This work is unreleased and does not complete
acknowledgement, silence, aggregation or HTTP
maintenance statistics. See [ALERT_MAINTENANCE.md](ALERT_MAINTENANCE.md).

## Incident silence (SIL-01/SIL-02, unreleased)

Migration 029 introduces incident-scoped, server-timed silence for 60 seconds to
7 days. Only firing events accept it; observation and condition transitions
continue. Maintenance OR silence suppresses intent and records exact causes.
Creation cancels outstanding jobs; expiry/cancel waits for fresh known evidence,
then sends one continuing-fault summary. Recovery closes active silence without
catch-up and future episodes are independent. Combined acknowledgement preserves
an existing first record or atomically creates it with silence. Immutable mutation
results support original-request replay after expiry, recovery, restart and full
restore. Protected management API/UI expose deadline, cancel, combined ACK,
history and exact delivery causes, with original-operation recovery across reload
and incident selection. Real receiver/browser acceptance covers both themes and
360/768/1440 px. Release integration and full capacity remain pending; no public fields change.
See [INCIDENT_SILENCE.md](INCIDENT_SILENCE.md) for semantics and acceptance gates.

## Notification aggregation foundation (AGG-01/ALT-05, in development)

Migration 030 retains logical event deliveries and adds bounded transport groups,
immutable send attempts and exact per-event links. Explicit policy
`aggregation_seconds` (5–300; zero/omission preserves immediate behavior) groups
by policy ID/revision, channel/provider and notification type, at most 10 events
per fixed window. Final checks remove recovered/muted members and defer stale
members independently; completion/history stays atomic. Text and JSON transport
bounds are enforced and unchanged retries keep their content/key. Transport-only
edits do not recreate technical incidents. Protected configuration/history and
context UI distinguish current outcomes from exact historical contents. New
policy forms show an added 30-second wait; zero disables it. Migration 031 freezes
the configured notification origin and content-link renderer while preserving
old payload/key identity. Password and GitHub return navigation open aggregate
content or individual events after authentication. Real receiver/browser checks
cover ambiguous acceptance, identical retry and both themes/three widths;
GitHub return navigation is locally simulated. Provider-specific acceptance,
broader administrative races, release integration and retention/capacity remain
pending. See [NOTIFICATION_AGGREGATION.md](NOTIFICATION_AGGREGATION.md).

Restart/full restore preserve window and transport identity. Actual late results
after takeover refine historical attempts without altering current jobs or clocks.
Final destination snapshots are private; their configuration version separates
changed retries. Older editors omit the field without disabling an existing
setting; an explicit zero disables it. These queue/receiver checks do not replace
release integration or complete AGG-01/ALT-05. Non-aggregate/service links and
M3–M5 remain in the original roadmap.
