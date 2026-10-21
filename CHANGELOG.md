# Changelog

All notable changes to MyProbe are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases
use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Target personal-use capacity acceptance at 20 nodes (usual deployment about
  15): five-to-twenty short checks followed by a 20-node uninterrupted 24-hour
  run. Larger workloads are optional; strict performance and recovery criteria
  and existing product/batch limits remain unchanged.

### Fixed

- Allow the required 20-node dense-history acceptance fixture, preserving the
  existing history density, disk headroom, strict SLOs and optional larger cases.
- Batch-preview cleanup respects the actual nanosecond expiry and thirty-day
  replay boundary when stored UTC timestamps have different fractional widths.
  Valid previews and replay results no longer disappear early, and expired
  previews release their per-user quota at the same instant as the apply check.

- Refresh the pinned Vue patch release and compatible PostCSS, Nano ID and
  source-map-js resolutions to remove the five package warnings reported by the
  npm dependency audit. Regenerate the browser bundle; deployment acceptance
  remains separate from the previously frozen runtime's capacity results.

- Bounded history reads avoid repeated node-visibility lookups for every sample
  and use compact, statement-local latency grouping keys. Public target IDs,
  weighted values, hidden-node permissions, time bounds and retention remain intact.
  Traffic accounting sorts counters once by exact time, timestamp spelling and
  original row ID instead of also sorting all history in SQLite; counter resets
  and retained totals keep their existing meaning.

- Public dashboards coalesce optional node-cache serialization into 250 ms
  windows while applying live updates immediately. Full/empty privacy snapshots
  and page exit flush current state; storage failures no longer interrupt event
  processing. Bounded reuse of sample/contact text reduces repeated locale work
  without changing displayed timestamps or their timezone behavior.

- Latency history seeks existing indexes by node, target, retained kind, tier and
  both time bounds before aggregating, preserving weights, missing durations,
  visibility and complete-bucket boundaries even after a target is changed or
  unassigned. Node history overlaps latency with metric/traffic reads, with a
  Server-wide limit of two node-history queries and cancellation/join on every exit.
  The shared connection pool, timeouts, schema and indexes remain unchanged.

- Idle notification consumers avoid writer reservations when no claim/recovery
  work exists, reducing maintenance contention and shared-pool starvation.
  Due jobs, expired leases, future system-job cancellation and final send
  authorization retain their existing transactional checks.

- History range reads use existing node/level/time indexes for both date bounds
  instead of filtering each node's full retained rollup history. Metric weights,
  latency statistics, traffic semantics and visibility are unchanged; no new
  schema or index build is required.

- Server startup and background history retention process closed time windows
  instead of holding one writer transaction for an entire backlog. Each pass
  atomically preserves rollups and traffic anchors, then yields before catch-up.
  Forward migration 049 stores small anchor references without backfilling raw
  history. See `docs/BOUNDED_HISTORY_RETENTION.md` for upgrade and scope limits.

- Closed certificate history no longer fills the alert evaluator's current rule
  workset or rewrites inactive definitions on every synchronization. Permanent
  identities remain available for exact deduplication and administration.
  Forward migration 048 adds partial runtime indexes; take an upgrade backup.

- Canceled SQLite queries release rows created before cancellation reaches the
  caller, allowing WAL checkpoints to advance after the pooled connection becomes
  idle. The driver update also prevents a late interrupt from reaching the next
  query on the connection. SQLite remains 3.50.4; no schema migration is required.

- Long collection periods no longer turn a reachable Agent into a false outage.
  Current socket heartbeats and independent HTTP fallback hellos persist contact
  without inventing metric samples or report ingress. Public stale-data status
  follows actual capture time; pages distinguish contact, capture and report receipt.

- Administrator node-history time selectors no longer trigger notification-link
  errors during login; pending notifications take priority over stale detail returns.

- Batch sampling updates reuse negotiated v2 configuration and application
  acknowledgements. Partial interface/mount settings preserve the omitted local
  selection. Management saves confirm desired configuration persistence.
- Exact metric replays refresh last contact without duplicating history. Distinct
  late samples remain durable without moving latest metrics/traffic backwards;
  conflicting payloads at one capture time are rejected.


### Added

- An offline `rollback-prepare` command creates a hash-verified new copy of a
  closed schema-013 backup with additive history lookup indexes. It checks
  unchanged table contents and preserves the untouched original and application
  migration receipts. See `docs/LEGACY_ROLLBACK_PREPARATION.md` before rehearsing
  the old application; this is not a down migration of a current database.

- Server startup logs identify pending restore, database open and history-retention
  phases with completion durations. The isolated joint-load fixture can retain
  bounded private process logs and distinguishes early exit from readiness timeout.
  Its 30-second readiness gate and production startup ordering remain unchanged.

- An operator-only offline upgrade rehearsal verifies forward migrations, exact
  legacy record preservation, integrity, reopen and a separate old-schema restore
  copy from a hash-verified closed backup. Default CI uses synthetic data; the
  opt-in never starts the Server or sends notifications.
  A separate application acceptance runbook records native isolated old/new
  startup, stored authentication, deployed Agent and large encrypted HTTP
  stage/restart recovery, with retained failures and restore-point limits.

- Node detail timelines show retained report observations, approximate reporting
  gaps and reboot hints. Authenticated timelines add node-owned incident phases,
  maintenance and configuration audit events with fixed-window pagination.
  Selecting an event marks and zooms the history curves. Migration 047 adds
  exact timestamp indexes without changing original evidence or Agent protocol.

- Integrated quantity editors preserve integer minor-unit prices, legacy billing
  cycles and unchanged expiry timestamps. Currency, bandwidth, traffic and time
  units are explicit in current node and alert-policy forms.
  Existing prices with missing currency remain editable and preserve raw minor
  units until an operator selects a valid currency.
- A read-only Windows soak observer distinguishes verified current process
  identities from stale running records, reused PIDs, missing handles and host
  restarts. New launchers retain process creation times; legacy records remain
  conservative. It never starts WSL, mutates a run, retries a test or infers test
  acceptance from an exit code.

- Private System Health compares official stable releases with the Server and the
  selected Agent's last advertised software version. Checks have a five-second
  bound, shared cache, manual cooldown and explicit unknown/stale/unavailable
  states. Daily automatic checks can be disabled with
  `MYPROBE_RELEASE_CHECKS_AUTO=false`. Official upgrade links and eligible Linux
  installer command references never execute updates or imply protocol
  incompatibility. No public fields, schema or Agent protocol changes.

- Mutually negotiated `network_probe.v1` now connects bounded native IPv4/IPv6
  TCP/ICMP execution to durable, connection-fenced task receipts. ACK follows
  atomic storage; identical replays do not insert twice and conflicting or stale
  results are rejected. Migration 045 freezes requested
  address family and packet count in target/epoch/task definitions; configuration
  v3 round-trips them while legacy v1/v2 imports retain auto/single-packet meaning.
  Administrator settings distinguish requested options from actual observations.
  Unsupported legacy sessions are skipped with explicit server provenance, never
  downgraded to a different family or used to invent an Agent sample/ACK. Strict
  negotiated contracts and bounded native IPv4/IPv6 executors have real platform
  tests. Versioned NQS2 archives retain observed family and weighted packet
  counts while reading NQS1 history as unknown. Public/admin/share comparison
  shows per-node and per-point evidence coverage, partial ICMP loss and TCP
  inapplicability without exposing addresses. Final integrated capacity and
  24-hour acceptance remain open; this draft is not a release.

- A Windows launcher can supervise a trusted WSL soak-test script independently
  of its launching shell, preserving heartbeat, process identities, logs and
  exit status. It does not restart failed runs or declare test acceptance.

- Internal network schedule epochs preserve effective assignment definitions and
  the known-history boundary from migration 042. Target group expansion is atomic
  with effective assignments and epoch records. Private snapshots retain target
  addresses; no new public field or Agent protocol change is introduced.
  Migration 043 adds unique planned tasks and transactional result identities;
  bounded dispatch preserves phase across restart and keeps uncertain sends
  distinct. Identical result retries receive ACK without duplicate history.
  Migration 044 adds bounded atomic raw/minute/five-minute network archival and
  monotonic retention floors. Internal queries distinguish missing and failed
  execution, weight successful RTT distributions and preserve jitter boundaries;
  retained quantiles include histogram error bounds and actual time resolution.
  An independent catch-up worker reports private health/export evidence. Public
  quality range-summary APIs now accept at most eight nodes through explicit
  public/share/admin authorization and privacy projections in one read snapshot.
  Companion time-series routes share a UTC grid capped at 2,000 points per node,
  generate both summaries and points in one historical scan, and distinguish
  unknown history from retention-resolution clipping. Protocol-unreported
  address families and packet loss stay unavailable. Target discovery now pages
  authorized current/retained aliases without target
  addresses. Public, administrator and protected-share comparison dialogs show
  up to eight observers, RTT/failure/coverage timelines, range quantiles/jitter,
  actual availability, timezone-aware custom windows and last completed evening
  presets. A real-backend Chromium fixture covers six theme/viewport combinations,
  scoped roles, missing data and keyboard access; full network capacity remains
  under validation. Agents are not assumed to retry lost results.

- Private node diagnostics separate desired sampling revisions, negotiated Agent
  application acknowledgements and actual persisted HTTP/WebSocket report ingress.
  Migration 040 fences revisions, connections and nonces without public fields.
  Optional v2 `config_apply.v1` preserves unnegotiated peers and released v0.2.1
  Agents; connection presence and dispatch never substitute for application proof.

- Administrators can preview a bounded, session-bound diagnostic JSON and download
  those exact bytes after a required audit write. Explicit export projections omit
  credentials, paths, addresses, names, payloads and arbitrary version labels.
  Private controls expose content scope, expiry and checksum; busy transport locks
  produce unavailable evidence without mutating pending work.

- Optional daily local encrypted backups use explicit time zones, durable date
  claims, owned file retention and independent passphrase files. Interrupted
  plaintext scratch is reclaimed only with authenticated cleanup authorization.
  Private asynchronous controls retain job/checksum history and separately verify
  isolated database and channel-credential recovery. Migration 039 stores backup
  jobs and independent failure reminders, sharing Provider retries and channel
  waits without synthetic nodes; unknown authorized sends are not retried.

- Administrator system and node diagnostics observe process identity, database
  metadata, actual runtime jobs, Agent connections, and encrypted backup generation.
  A private health page restores node context through login and reload and separates
  unavailable evidence from zero counts; delivery and recovery evidence retain
  their distinct meanings.
  Current service runtime integration adds HTTP dispatch/receipt-grace evidence,
  actual cleanup-loop/batch observations and read-only durable queue counters.
  Missing send configuration is separate from a running technical evaluator.

- Service alert mapping and overwrite previews use bounded 25-item pages and a
  shared searchable destination picker. Explicit mapping templates support large
  batches without automatic matching; compact pending-request recovery retains
  the original identity and remains compatible with old full-preview caches.
  An opt-in 1000-service/channel browser fixture verifies the full batch and replay.

- Private versioned HTTP service alert export/import explicitly maps existing
  destination services and channels, previews all overwritten reminder parameters,
  rejects stale destination revisions, and commits the whole batch with durable
  request replay. Certificate-only and cross-provider configurations are supported.
  Migration 037 retains original import results across full backups; browser refresh
  and lost responses reuse the original request. Credentials and runtime history
  are excluded, and existing configuration v1/v2 and host policy bundles are unchanged.

- Protected HTTP service diagnostics restore the service, observer and time range
  after login or reload. Bounded history pages separate real failures from
  unexecuted, pending and missing checks, with mature raw-slot counts in the same
  read snapshot. New service notifications capture a separate diagnostic link
  through retries and full restore; Webhook templates and Telegram/Discord/SMTP
  render it. Migration 036 adds a pagination index. No private targets, bodies,
  credentials, unmeasured phase timings, public fields or Agent changes are added.

- Private HTTP statistics optionally exclude effective maintenance-window unions
  by planned slot and frozen observer membership. Raw and removed counts share
  one read snapshot; overlapping windows cannot double-count, cancellation clips
  future coverage, and newly backdated windows cannot erase earlier history.
  The browser explicitly defaults to exclusion, supports raw comparison, and
  preserves null rates when every mature slot is excluded. API omission keeps
  the original raw behavior; no schema, public-node or Agent protocol change.

- Opt-in certificate expiry warnings independently enable 14/7/1-day reminders
  per HTTP service. Migration 035 persists service/fingerprint/threshold identity,
  queue and final-send authorization across observers and full database restore.
  Only the most urgent currently due tier is notified; missed tiers are skipped.
  Verified fresh evidence drives warnings, complete fresh success confirms
  certificate replacement, and expiry/rotation never sends technical recovery.
  Private settings/history show warning severity, expiry, dedup state and the
  original event link without changing service health or public/Agent contracts.
  Existing outbox retries and provider receipt limitations remain in effect.

- Private HTTP service lists and current-state detail now declare the
  any-required-observer-failure policy and separate success, failure, unknown and
  current observer coverage. Partial success or an empty observer set never
  becomes healthy. Summaries share service-alert freshness/capability evaluation,
  use bounded current-revision database/session snapshots, and omit target specs.
  Browser expiry/read failure removes old healthy presentation and allows refresh.
  Existing Agent, public-node, schema and notification contracts are unchanged.

- Integrated opt-in HTTP checks and the durable incident platform on the service
  development branch, preserving execution/origin configuration, private service
  management and notification navigation. Separate HTTP/incident development
  database upgrade fixtures preserve pending work and original history. Service
  certificate reminders and maintenance statistics remain open.
- Added private HTTP service alert settings and per-observer incidents to the
  existing state machine and delivery queue. Actual current-revision failures and
  recoveries notify through configured channels; missing, pending, offline,
  unsupported, disabled-execution and stale observations remain unknown. Event
  history and notifications retain safe service identity and original-event links.
  Management changes fence stale observations and in-flight receipt authority;
  existing silence, node maintenance, retry and full database restore apply.
- Added service alert configuration/history management, dual revision conflict
  checks and responsive controls. Portable JSON export explicitly excludes alert
  settings/history; complete database backup includes them. Public-node and Agent
  payloads do not gain service details.
- New individual incident notifications capture an authenticated event URL when
  queued. Webhook JSON includes `incident_path`, custom Webhooks support
  `{{incident.url}}`, and Telegram/Discord/SMTP append a formatted event entry.
  Retry and full restore retain the captured URL; old queued payloads keep their
  original bytes. Configure `MYPROBE_NOTIFICATION_ORIGIN` for absolute links.

### Fixed

- Joint load validation separates bounded preparation from the full requested
  measurement duration, retaining child processes across the phase transition.
  Evidence records preparation time and the measurement/finalization deadline;
  slow history generation cannot silently consume a 24-hour observation budget.
- macOS IPv6 ICMP uses bounded Echo Requests without treating BSD `ping6 -W`
  (an FQDN/node-information option) as the IPv4 response wait. The shared task
  deadline still bounds completion; interrupted summaries retain unknown counts.
  Native IPv4/IPv6 TCP, two-packet ICMP and scheduled three-packet Agent/Store/ACK
  gates are added for Apple Silicon and Intel macOS, with skips excluded from
  acceptance and source-bound evidence artifacts.

- Live node updates read only the visible node being published, avoiding repeated
  whole-dashboard assembly for every report or latency result. Shared projection
  preserves public masking, metadata omission, traffic and visibility semantics;
  full snapshots and API lists stay complete, with no schema or protocol change.

- Committed service-alert imports replay their original result through a read-only
  point lookup instead of waiting for unrelated management writers. New requests
  still recheck inside the original atomic transaction; receipt retention, caps,
  request conflicts, authentication and CSRF remain unchanged.

- History retention now locates exact epoch buckets and filters eligible ranges
  instead of scanning each node's year of rollups. Migration 041 adds indexes and
  private virtual nanosecond sort keys while preserving original sample/JSON bytes.
  Raw history/latency ranges, latest results and counter anchors correctly order
  fractional timestamps; samples just before a minute no longer round into it.

- Agent welcome sampling periods now take effect. Independent collection and
  reporting loops retain only the latest successful sample, discard old-generation
  samples after period changes, and avoid repeating the same cached sample.
  Period-only legacy messages preserve local interface/mount selection; partial
  omission preserves fractional local durations.

- HTTP history cleanup catches up in separate 1000-row transactions, yielding
  between batches instead of leaving growing expired backlogs behind an hourly
  limit. Failed batches roll back and retry with backoff; clock rollback preserves
  the published floor. Migration 038 indexes closed epochs. Host rollups, original
  import replay and public/Agent contracts retain their meanings.

- Forward-only migration 034 preserves legacy rules, events, states, receipts and
  queued incident work while adding managed service bindings/settings. The rebuilt
  rule-kind constraint also accepts existing memory, disk and latency kinds.
  Service bindings cannot enter host-scope migration or direct host-rule editing.

- Delivery completion now rechecks current policy, rule, node and channel state
  before applying a receipt. Management changes during an actual request cancel
  obsolete individual/aggregate jobs without waiting for evaluation, preserving
  real attempt results while revoking retry and repeat-clock authority. A wholly
  revoked aggregate cannot extend channel waits; valid remaining members proceed.

- Provider retry waits now persist per notification channel and gate aggregate,
  individual and manual test sends. Concurrent results cannot shorten a wait;
  replacing provider/configuration isolates old results. Known-unsent reservations
  refund their retry budget while preserving paginated history. Admin views show
  the last channel deadline and distinguish history entries from send budget.
  Forward-only migration 033 preserves attempt IDs and aggregate membership while
  allowing history to exceed five entries; public and Agent contracts are unchanged.
- Telegram delivery requires a valid Bot API message confirmation; Discord requests
  and validates saved-message confirmation. Malformed/empty successful responses
  remain unknown, and provider body/header retry delays are honored without leaking
  response details. Aggregate names/excerpts are escaped for rich formatting,
  Discord keeps its content link while bounding text, and SMTP now sends encoded
  UTF-8 subjects and quoted-printable MIME over verified TLS/STARTTLS.
- Aggregate JSON can avoid optional HTML-escaping overhead while retaining all
  references. Invalid/oversized logical rendering now records an atomic known-unsent
  failure and lets other groups progress. History explains logical content versus
  provider formatting and local failure versus unknown reception.
- Migration 032 captures new groups' private custom-Webhook template time so
  timezone changes across restart/restore do not alter an unchanged retry body.
  Old groups preserve legacy rendering/key identity; keep their timezone until
  pending work drains. No public/API/Agent fields are added.

- Maintenance decision and delivery-link writes now use bounded batches within
  the original transaction, reducing repeated SQL work under overlapping windows.
  Later-batch failures roll back earlier records and preserve queued-task state.

- A provider response arriving after maintenance cancels an in-flight notification
  now updates that attempt instead of losing the result to lease revocation. The
  task stays canceled, with no retry or repeat-clock change. Delivery details
  explain that a canceled task may already have reached its channel.

- Policy-import confirmation generates random request IDs without requiring the
  secure-context-only randomUUID API, supporting direct HTTP browser deployments.

- Invalid tab recovery records no longer break policy import rendering. The page
  preserves the uncertain record and offers explicit clearing after result review.

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

- Notification aggregation foundation (migration 030): explicit policy windows,
  bounded per-policy/channel/type shards, durable transport attempts and exact
  per-event attempt links. The worker revalidates each member before sending;
  unchanged retries preserve content/key and stale members wait independently.
  Real loopback receiver measurements cover 5/50/100-node storms. Protected
  management configuration/history distinguish current event results from actual
  attempt content, including unknown outcomes. New policy forms show a 30-second
  wait, while old settings are preserved. Migration 031 keeps old payload/key
  identity and freezes optional `MYPROBE_NOTIFICATION_ORIGIN` for new notification
  content/event links. Login returns to the original content; real receiver and
  browser checks cover identical ambiguous-send retries and both themes/three
  widths. Local provider protocol acceptance passes; live account/client rendering,
  shared-channel rate backpressure, release/capacity acceptance remain pending;
  this is unreleased.
  Restart/full restore retain windows and keys. Actual late results after a parent
  takeover refine history without changing successor jobs or clocks. Destination
  configuration is read from the final send snapshot and its version participates
  in the key. Older policy editors preserve the existing aggregation setting.

- Incident silence intervals, revisioned cancellation, atomic optional
  acknowledgement and replay history (migration 029). Existing observation/outbox
  paths combine maintenance and silence, wait for fresh evidence after the final
  suppression and close event silence on recovery. Protected management API/UI
  expose deadline, actor/reason/history and exact delivery causes. Original create
  and cancel requests recover across reload/incident selection; real receiver
  and browser acceptance covers both themes/three widths. This is unreleased.

- Internal incident acknowledgement record and protected management API (migration
  028), with first-actor history, private notes, atomic audit and identical-request
  replay. Acknowledgement does not silence, resolve or alter notification jobs.
  Private event lists expose author/time summaries only. Processing details provide
  note/history, original-request recovery across reload/selection and explicit
  first-winner conflicts. Real receiver/browser acceptance verifies continued
  notifications and private note isolation. This is not yet a released feature.

- Recorded maintenance context for incidents and individual canceled deliveries
  (migration 027). Protected paginated APIs and incident details distinguish
  current frozen-member scope from actual evaluation/gap/worker decisions, retaining
  links through restart and backup restoration without inferring older causes.

- Internal maintenance-window storage (migration 024), frozen node membership,
  revisioned cancellation and historical half-open interval queries. Event enqueue,
  delivery claim and pre-send checks suppress maintenance notifications, including
  observation gaps spanning a window. An authenticated, uncached management list
  API provides bounded ID-cursor pagination. This is not yet a released feature.

- Maintenance member preview API resolves explicit nodes or all/any tag selections
  to named members, with session/CSRF protection and a 100-member limit. Preview
  does not create a window or reserve members for a subsequent operation.

- Full maintenance preview returns normalized window fields, named membership and
  a confirmation digest, sharing validation with internal window creation.

- Internal confirmed maintenance creation atomically checks current membership and
  saves the window, selector provenance, audit and retry identity (migration 025).
  Protected creation/cancellation APIs bind actors to authenticated sessions,
  preserve cancellation history (migration 026), and replay confirmed requests.
  Ended windows cannot be newly created/canceled; committed retries still replay.

- Notification-center maintenance UI with local-time input, explicit/tag selection,
  preview confirmation, frozen-member history, statuses and cancellation. Uncertain
  create/cancel requests survive tab reload for identical retry; unavailable or
  corrupt browser storage blocks new mutations until resolved. Maintenance-canceled
  deliveries explain suppression and link to the maintenance page.

- Policy-import confirmation displays readable scope, destination names, threshold
  units and timing fields instead of requiring users to inspect raw JSON.

- Authenticated policy-import preview API with explicit destination mappings,
  strict bounded input and transactional dry run. Submission binds the source
  content to the preview digest, revalidates destinations, and returns original
  results for committed retries. The policy page provides file transfer, explicit
  reference mapping and confirmation, retaining uncertain requests across tab reloads.

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

- Opt-in Server HTTP execution with `MYPROBE_HTTP_PROBES_ENABLED=true`, defaulting
  to disabled, and real Agent/scheduler/result-storage coverage for policy denial.
  Maintenance integration and HTTP incident/certificate notifications remain pending.

- HTTP per-observer statistics panel with sample counts, success/coverage rates,
  null-denominator labels, explicit effective ranges and maintenance limitations.

- Administrator per-node HTTP statistics API with bounded ranges, query timeout,
  explicit history boundaries and maintenance-adjustment status. No SLA claim is made.

- HTTP statistics disclose the known schedule-history boundary and exclude
  unreconstructable pre-migration revisions from the effective coverage window.

- Administrator HTTP recent-result panel with failure categories, explicit waiting
  and missing states, and expandable certificate summaries. Status uses the Server
  observation time; empty lists do not imply service health.

- Bounded administrator HTTP observation queries with private-spec exclusion,
  null pending results and exact timestamp ordering (migration 020).

- Administrator HTTP Services tab with configuration forms, observer selection,
  pagination, conflict-preserving edits and explicit history-deletion confirmation.
  JSON numeric assertions retain exact tokens; the page shows when Server execution
  is disabled. Result and certificate views remain under development.

- Configuration import preview displays HTTP service create/update counts and
  explains assignment replacement; export copy identifies private URL/assertion data.

- Configuration snapshot v2 transfers HTTP services and node assignments, with
  transactional dry-run/merge counts and v1 import compatibility. HTTP results,
  revision history and Agent-local network allowances are not transferred.

- Paginated administrator HTTP service summaries and revision-checked deletion,
  including associated task/history cleanup. Summary responses omit target URLs.

- Private HTTP service create/read/replace APIs with administrator authentication,
  CSRF protection, no-store responses, optimistic revisions and sanitized audit
  metadata. Saving configuration does not enable production HTTP dispatch.

- Bounded 31-day HTTP task/history cleanup with a durable retention floor
  (migration 019). Internal statistics expose requested and effective ranges and
  exclude deleted history from missing-sample counts.

- Internal HTTP per-observer statistics with separate success, failure and missing
  counts, null empty rates, consistent read snapshots and a conservative result
  maturity window. Public statistics endpoints remain pending.

- HTTP schedule revision history and bounded half-open expected-slot counting
  (migration 018), preserving the denominator when checks are not dispatched.
  Availability and coverage APIs are still under development.

- HTTP scheduler foundation with configuration-anchored slots, bounded dispatch
  workers and no replay of missed checks. Production startup and coverage
  accounting remain pending integration.

- Private HTTP service configuration storage with revision-checked updates and
  transactional node assignments (migration 016). Service APIs and scheduling
  remain under development and are not enabled by this storage foundation.
- Durable HTTP task snapshots and conditional result storage (migration 017),
  rejecting wrong-node, expired, obsolete-revision and duplicate results. Transport
  dispatch and ingestion are integrated behind a default-off gateway gate;
  production scheduling remains pending.

- Opt-in Agent v2 WebSocket endpoint with explicit version isolation and capability
  negotiation. Existing v1 clients remain supported; HTTP execution is not enabled.
- Agent prefers v2 and falls back to v1 when that endpoint is absent (404/405),
  while preserving v1 HTTP report fallback and refusing authentication-error downgrade.
- Bounded Agent HTTP executor with TLS validation, decoded-response limits and
  connection-scoped cancellation. Local `-http-private-cidrs` and
  `-http-additional-ports` options (and matching `MYPROBE_HTTP_PRIVATE_CIDRS` /
  `MYPROBE_HTTP_ADDITIONAL_PORTS` environment variables) configure target policy.
  Server HTTP scheduling remains disabled pending result persistence.
- Public dashboard search, intersecting tag/status filters, explicit target-based
  sorting, shareable query links and remembered presentation preferences.
  The existing network comparison entry retains a visible icon on narrow screens.
- A keyboard-accessible node comparison table with a sticky identity column on
  small screens, and throttled live ordering that pauses during list interaction.
- Distinct waiting, interrupted-report and stale-data states, public attention
  reasons, and filtered totals that exclude stale samples from live rates.
- Transactional node batch configuration previews with ownership, ten-minute
  expiry, revision conflicts, persistent idempotency and atomic audit recording.
- Administrative batch selection across pages for tags, visibility, sampling
  configuration and additive/removal/replacement probe-target assignment.
- Accessible node ordering with up/down controls, Alt+arrow shortcuts, position
  announcements and explicit cancel/save. Complete-list saves reject stale order
  changes, commit ordering and audit atomically, and safely replay lost responses.

- Node-detail API foundation: a non-cacheable single public-node endpoint reuses
  visibility and privacy filtering. Public and scoped-share history endpoints accept
  bounded absolute times, return interval metadata and omit partial retention buckets.
  A fixed public detail page supports URL-persisted time selection, separate linked
  charts, gap display and zoom/reset controls. Authenticated administrator detail
  supports hidden nodes and preserves the destination across login and session expiry.

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
