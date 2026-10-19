# MyProbe product specification

## Product principles

1. Provide a calm, information-first Apple-inspired monitoring experience without
   copying third-party source, assets, branding, or implementation details.
2. Keep the agent small, outbound-only, and safe by default.
3. Make every public metric explainable from a typed protocol field.
4. Preserve a one-command deployment and a single server binary release.
5. Treat offline state, traffic accounting, and notifications as correctness-critical.

## Feature parity matrix

The owner's deployment target is personal use with at most about 50 nodes
(clarified 2026-10-19). Required capacity validation progresses from five to
50 nodes and then a continuous 24-hour run. The existing correctness, latency
and resource gates remain; 100/500 nodes are optional expansion work rather than
release prerequisites. Historical large correctness fixtures retain their scope.
See [self-use capacity and acceptance](SELF_USE_CAPACITY.md).

Private System Health now includes official stable-release awareness for the
Server and selected Agent's last advertised version, with explicit unknown,
stale and unavailable states. Daily automatic checks can be disabled; manual
checks share a bounded cached request and cooldown. Version ordering never
asserts protocol incompatibility or capability support. Fixed official upgrade
instructions and eligible Linux installer commands are advisory only; no remote
update execution is added. See [release awareness](RELEASE_AWARENESS.md).

The network evidence work package now persists requested family/packet options in
migration 045 and configuration v3. Private target controls expose those requested
settings; omitted fields on a legacy rename preserve the existing definition.
An unsupported Agent connection is explicitly skipped without sending a task in
another family. Native Windows/Linux IPv4/IPv6 and isolated silence/no-route tests
validate the executor. Negotiated live execution, atomic connection-fenced
receipts, backward-compatible NQS2 retention and comparison evidence are now
connected. Per-node and per-point observed family/count coverage remains unknown
for legacy data; incomplete ICMP coverage is labelled partial and TCP loss is
inapplicable. Final integrated capacity and 24-hour acceptance remain open. See
[network probe evidence](NETWORK_PROBE_EVIDENCE.md).

Network quality comparison is available in the public dashboard, administrator UI
and authenticated shares. Scoped target discovery and one batched response compare
up to eight observers using aligned RTT, execution-failure and coverage timelines,
range quantiles/jitter, actual history bounds and explicit time zones. Real-backend
browser checks cover both themes at 360/768/1440 px. Unreported address families and
packet counts remain unavailable; final integrated capacity, broader platform
coverage and long-soak acceptance remain open. See [network quality UI](NETWORK_QUALITY_UI.md).

Scoped alert-policy inheritance (ALT-02) is in development. Its internal resolver
and revisioned persistence support global, tag and explicit-node scopes, conflict
validation and snapshot-based effective-policy previews. Each runtime tick maps
legacy rules and applies policies atomically before incident evaluation. Management
APIs and UI report pending/ready/error preparation with the last successful time;
saving a policy is distinct from applying it or delivering a notification. Failed
startup preparation prevents notification workers from starting. Live browser,
full upgrade/restore, capacity and configuration-transfer checks remain release
gates. See [alert policy design](ALERT_POLICIES.md).
HTTP monitoring is experimental and opt-in via the Server environment variable
`MYPROBE_HTTP_PROBES_ENABLED=true` (default false; restart required). The current
implementation includes negotiated v2 execution, bounded GET/HEAD checks, private
administrator configuration/results, certificate evidence and per-observer
statistics. Policy rejection counts as unobserved, not a target failure. See
[HTTP service monitoring](HTTP_SERVICE_MONITORING.md) for contracts and evidence.
Live HTTP GET/HEAD and six theme/viewport browser CRUD/result flows have opt-in
integration evidence, including live HTTPS verification failures. Service settings
and per-observer fault/recovery now reuse the incident state machine, suppression,
Outbox and original-event links. A real online Agent checking a failing site then
a recovered site verifies both persisted receipts and protected browser navigation.
Full database restore retains service settings, event identity and pending delivery
evidence. Missing, pending, stale or unsupported observations remain unknown,
including when the current Gateway cannot execute HTTP. See
[service incidents](SERVICE_INCIDENTS.md) for revision, privacy and timing contracts.
The private service list/detail now summarize current observers using
any-required-observer-failure: one failure degrades, partial success remains
unknown, and only all fresh successes are healthy. Current observer coverage is
separate from historical slot coverage/success rate. Snapshot expiry and read
failure revoke old healthy presentation; see [current service health](SERVICE_HEALTH.md).
Certificate reminders now independently enable verified 14/7/1-day warnings,
persistently deduplicated per service/fingerprint/tier across observers and full
restore. Only the most urgent reached tier is sent; missed tiers are skipped.
Complete fresh success confirms replacement, and expiry/rotation never sends
technical recovery. Warning state does not change current health. Private history
distinguishes queueing, final authorization and real receipts; see
[certificate reminders](CERTIFICATE_REMINDERS.md) for migration 035 and live TLS evidence.
Private statistics now optionally exclude the union of effective frozen-member
maintenance windows by planned slot, retaining raw and removed counts in one read
snapshot. Creation bounds prevent backdated historical exclusion; cancellation
clips the future interval. API default remains raw, while the browser explicitly
requests exclusion and exposes both counts. All-excluded denominators stay null;
this does not change current health or claim continuous-time SLA. See
[maintenance statistics](HTTP_MAINTENANCE_STATISTICS.md).
Protected service/observer/time-range diagnostics now restore context through
password/session login, reload and the existing OAuth navigation storage. New
service notices durably capture a diagnostic link; bounded pages distinguish
failures, unexecuted checks and absent results using existing admitted evidence
and mature raw statistics from one read snapshot. No target/body/credentials or
unmeasured phase timings are returned. See [service diagnostics](SERVICE_DIAGNOSTICS.md)
for migration 036, cursor/privacy and navigation contracts. Capacity, real OAuth
and public-provider/client release acceptance remain incomplete.
Configuration enablement alone does not start execution; v1 Agents remain supported
for their existing capabilities and never receive HTTP tasks.

Portable configuration v2 includes service definitions and observer assignments,
but excludes service alert settings, scope policies, incidents and delivery history.
Use a complete encrypted database backup to transfer those records; importing a
service definition changes its revision and administratively ends old incidents.
Service alert parameters now have a separate private `myprobe-service-alerts` v1
bundle with explicit service/channel mapping, read-only before/after preview,
destination revision fences and atomic persistent request replay. It covers fault
and certificate-reminder settings, including disabled and cross-provider choices,
without credentials or historical records. Migration 037 preserves replay identity
through full backups; browser recovery confirms the original request after lost
responses or refresh. See [service alert transfer](SERVICE_ALERT_TRANSFER.md).
Source mapping and previews now render 25 items per page, with one searchable
destination picker and an explicitly filled mapping-file template. Paging never
limits the complete atomic batch. Compact recovery retains the original request
and digest and accepts legacy full-preview caches. A 1000-service/channel local
transfer check verifies every stored row and lost-response replay; see
[capacity evidence](SERVICE_ALERT_CAPACITY.md). This is not Agent throughput or
the full-system 24-hour acceptance.

HTTP retention now independently catches up in bounded 1000-row transactions,
yielding between batches and retrying errors with backoff. A monotonic 31-day
floor and both deletions remain atomic per batch; migration 038 indexes closed
epochs. Host rollups and permanent replay/deduplication records are preserved.
Idle single/aggregate/system notification consumers use read-only negative
preflights while retaining the original positive claim and lease-recovery
transactions; future system jobs still undergo stale-channel cancellation.
See [idle notification claims](IDLE_NOTIFICATION_CLAIMS.md).
History range reads explicitly select the two supported rollup levels so the
existing node/level/time indexes can seek both bounds; see
[rollup range access](HISTORY_RANGE_INDEXES.md).
Host history now also uses closed-window startup/background transactions with
durable traffic-anchor references (forward migration 049). A successful
maintenance job reports one committed batch, not complete backlog exhaustion.
See [bounded history catch-up](BOUNDED_HISTORY_RETENTION.md) and
[cleanup contracts and local capacity evidence](HTTP_RETENTION_CAPACITY.md);
this does not complete full-system capacity or the 24-hour acceptance.

| Area | Requirement | Acceptance evidence | Status |
| --- | --- | --- | --- |
| System health | Private runtime/version/database/retention/transport, schedulers, queue and node diagnostics; negotiated sampling application and actual report ingress, with old-Agent capability warnings; full recovery/capacity remain separate | `docs/SYSTEM_HEALTH.md`, `docs/AGENT_RUNTIME_EVIDENCE.md`, actual Server/browser/Agent and privacy/lifecycle tests | In progress |
| History retention capacity | Exact bucket/range indexes, original payload-preserving nanosecond sorting, cutoff/order/bucket regressions; dense year-backed maintenance measurements separate from live-system acceptance | `docs/HISTORY_RETENTION_CAPACITY.md`, migration 041 and Store/restore tests | In progress |
| Permanent service import replay | Read-only committed-result lookup, transactional miss recheck, full 10000 × 1000-result storage and consistent restore; retention/cap identity unchanged | `docs/SERVICE_REPLAY_CAPACITY.md`, Store/API boundary and concurrency tests | In progress |
| Joint runtime load | Real main Server and Client.Run with synthetic metrics, actual TCP/HTTP execution, isolated default-sender notification failure/recovery, reconnect/restart, explicit rendered/protocol subscriptions and observed resource controls; short/current-run fixtures do not certify dense history or 24 hours | `docs/JOINT_RUNTIME_LOAD.md`, opt-in Agent fixture and isolated Linux runner | In progress |
| Live node publication | Single visible-node and metadata lookup shares the existing public projection; updates avoid assembling every node while full snapshots remain complete | `docs/NODE_PUBLICATION_CAPACITY.md`, Store privacy/parity and Hub visibility tests | In progress |
| Runtime reconciliation | Opt-in test-only HTTP/WebSocket observer compares real report attempts, matching Server ACKs and stored content; final real HTTP markers check every node DOM on five rendered pages without reload | `docs/RUNTIME_RECONCILIATION.md`, negative accounting cases and joint fixture | In progress |
| Dense joint history | Full retained-year synthetic history for every workload node; real public 30-day/year responses checked while Agents, tasks, notifications and browsers run; seed rows excluded from live accounting | `docs/DENSE_JOINT_HISTORY.md`, history validation negatives and opt-in joint fixture | In progress |
| Joint resource observation | Independent RSS/WAL/CPU sampling across slow API requests and Server restart, with actual cadence and unavailable samples reported | `docs/JOINT_RESOURCE_OBSERVATION.md`, blocked-request and concurrent restart observation tests | In progress |
| Network quality | Migrations 042/043 persist private plans and atomic task/result identity; 044 adds bounded archival, weighted histograms and gap-aware jitter. At most eight nodes share one snapshot through explicit public/share/admin summary and bounded time-series APIs. One-year single-observer storage/restore has native evidence; target discovery, comparison UI and full runtime capacity remain open | `docs/NETWORK_QUALITY.md`, `docs/NETWORK_QUALITY_API.md`, range/auth/privacy/grid/mixed-tier/restore regressions, native archive evidence | In progress |
| Scheduled backup | Daily time-zone/date claims, independent secret file, authenticated local retention, durable failure reminders and isolated DB/channel recovery evidence | `docs/SCHEDULED_BACKUPS.md`, `docs/BACKUP_CAPACITY.md`, `docs/ONLINE_BACKUP_LOAD.md`; Store/Provider/API, process-kill, actual main/browser, physical ENOSPC, large encrypted DB/RSS and opt-in bounded online overlap; cross-machine and production failover remain separate | In progress |
| Diagnostic export | Whitelist-only private JSON preview, session-bound frozen bytes, short bounded retention and mandatory audited download | `docs/DIAGNOSTIC_EXPORT.md`; version/field privacy, expiry/concurrency, source mutation, audit failure and actual main/browser evidence | In progress |
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
| Batch administration | Owned previews, atomic configuration updates, revision conflicts, replayable retries and cross-page selection | Store/API/Agent regressions and `scripts/node_batch_browser_test.cjs`; see `NODE_BATCH_MANAGEMENT.md` | Implemented |
| Agent intervals | Independent collection/report timers; negotiated v2 configuration/application acknowledgements; cached sample deduplication | Agent/gateway/store configuration tests; released v1/HTTP retain compatibility | Implemented |
| Node ordering | Keyboard up/down and Alt+arrow movement, cancel/save, atomic conflict detection and replay | Store/API order tests and `scripts/node_order_browser_test.cjs`; see `NODE_ORDERING.md` | Implemented |
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

### Discovery and comparison

The dashboard supports case-insensitive name/tag search intersected with tag and
state filters. Attention means interrupted reports, stale samples, current CPU,
memory or any disk at least 90%, expired nodes, or expiry within seven days. These
are explicit public display heuristics, not administrator alert policy disclosure.
Waiting nodes do not count as interrupted reports or healthy resource samples.

Sort by configured order, name, CPU, memory, a selected latency target, or expiry.
Missing/stale numeric values sort last and live metric ties use node IDs. Configured
order ties follow stored UTF-8 name order then ID, matching the administrator list.
Dynamic ranks refresh
every five seconds and pause while a pointer, keyboard focus or history dialog is
using the list. Filter changes apply immediately; metrics continue updating.

Query parameters preserve search, tag, state, sorting, target and cards/table view;
explicit URL values override locally remembered view, sort and target preferences.
Unknown targets/tags remain explainable and can be cleared. Copying a query link
includes defaults so the recipient's preferences cannot override the shared view.

The table offers name/history access, state/reasons, CPU, memory, upload/download,
selected-target latency and expiry. Narrow screens scroll the table horizontally
while retaining the identity column. Filtered and total node counts are distinct.
Browser disconnection is a global connection notice, not evidence all nodes failed.

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

### Administrative quantity inputs

Node prices are entered as decimal amounts and stored as integer minor units using
the currency's fraction digits. Changing currency preserves the entered amount,
without exchange-rate conversion. Standard billing cycles have presets; existing
custom cycle text remains editable. Expiry inputs identify the browser timezone,
store UTC, and preserve the original timestamp when unchanged.

Bandwidth, traffic and alert duration inputs expose unit selectors. Mbps uses
decimal bits, GB/TB decimal bytes, and GiB/TiB binary bytes. Unit changes preserve
the stored integer, and values exceeding JavaScript's safe integer range or below
the base-unit precision are rejected. Non-terminating conversions retain the
previous unit with an explanation. Existing API, schema and Agent protocol stay
unchanged.

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

Agent contact and sample freshness are independent. Authenticated current-session
heartbeats and HTTP hellos refresh contact without fabricating metric history,
report ingress or configuration application. Current Agents keep five-second
contact during long collection periods and stalled socket dialing. Dashboard
freshness uses capture time and both configured periods; contact-only nodes still
await their first sample. See [runtime evidence](AGENT_RUNTIME_EVIDENCE.md).

History API queries accept the existing range presets or paired RFC3339 `start`
and `end` parameters (maximum 365 days, no future end). Responses describe the
half-open interval, bucket width and complete-retention-buckets-only policy.
All resource, latency and traffic reads exclude samples at or beyond the end.
Partially overlapping retained buckets are omitted, not reconstructed as precise
samples. Public and authenticated administrator node detail pages provide custom
time selection and separate linked charts. Administrator detail supports hidden
nodes and retains the destination across login; public and share APIs retain their
visibility boundaries. See [NODE_DETAILS.md](NODE_DETAILS.md).

Node detail timelines link retained report observations and reboot hints to the
history curves. Public timelines contain only approximate observation evidence;
authenticated timelines add node-owned incident phases, maintenance and saved
configuration operations without copying private payloads. Fixed-window cursors,
bounded raw scanning, retention limitations and migration 047 are described in
[NODE_TIMELINE.md](NODE_TIMELINE.md). A saved configuration is not Agent application
proof, a reporting gap is not host downtime, and a reconnect is not a reboot.

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
GitHub return navigation is locally simulated. Local provider protocol checks cover
Webhook, Telegram's three parse modes, Discord and verified TLS/STARTTLS SMTP.
Frozen logical history differs from escaped/clipped HTTP or encoded MIME envelopes;
local rendering failures are known unsent and complete atomically without receipts.
Telegram/Discord require valid message confirmation and honor body/header delays.
Migration 032 captures new groups' private template time across timezone-changing
restore; old groups keep their legacy behavior and require a stable timezone while
draining. Live account/client acceptance, shared-channel rate backpressure,
broader administrative races, release integration and retention/capacity remain
pending. See [NOTIFICATION_AGGREGATION.md](NOTIFICATION_AGGREGATION.md).

Restart/full restore preserve window and transport identity. Actual late results
after takeover refine historical attempts without altering current jobs or clocks.
Final destination snapshots are private; their configuration version separates
changed retries. Older editors omit the field without disabling an existing
setting; an explicit zero disables it. These queue/receiver checks do not replace
release integration or complete AGG-01/ALT-05. Non-aggregate/service links and
M3–M5 remain in the original roadmap.

### 通知渠道共享等待补充（开发分支，迁移 033）

渠道维度持久保存 Provider 等待期限，覆盖聚合、单事件与手工测试；自身任务期限与渠道期限同时满足才允许发送。等待中的任务不占领取批次和发送预算；领取后发现等待则原子返还预算，保留唯一历史序号与分页诊断。配置版本隔离旧响应，并发结果只延长等待。更换配置清除等待，改名和启用状态保留等待。授权后正在发送的请求仍可能完成，不承诺 Provider 全局限速或恰好一次。

仅管理员接口新增渠道期限与尝试序号，历史默认每页 25 条、最多 100 条；渠道版本和发送授权不公开。管理界面区分五次发送预算与不限于五条的历史，并提示到期仍需满足其他发送条件。迁移、重启/恢复、真实本地 HTTP 并发及明暗主题三宽度浏览器均需验收。该功能属于 AGG-01/ALT-05 的持续开发，不代表整条路线已发布。

### 单事件通知返回原事件（开发分支）

新单事件任务将事件地址固定到入库正文，故障、重复提醒和恢复共享原事件身份。Webhook 默认 JSON 增加 `incident_path`，自定义 Body 支持 `{{incident.url}}`；Telegram/Discord/SMTP 提供格式化入口。地址来自已验证的通知 origin，没有 origin 则提供相对路径；入口要求管理员登录并显示事件当前状态。旧任务保持原正文，新任务的重试、重启和完整恢复保持地址。无数据库迁移、公开节点或 Agent 协议变更。实际本地 Webhook 与浏览器登录、各渠道本地报文、完整恢复和生命周期已验收；M3 服务通知、公网账号/客户端展示及发布集成继续待实施。

### 管理变更后的回执权限（开发分支）

单事件与聚合的当前租约在结果写回事务内重查当前事件、规则、策略范围与渠道。管理变更使任务失效时，逻辑任务取消，实际尝试历史保留；不恢复重试或推进失效事件的提醒时钟，不依赖下一次评估先运行。部分有效成员继续正常完成，全部失效的组不再延长共享等待。维护/静默动作与技术事件状态仍沿用既有语义。真实本地接收端覆盖八类操作、四类响应及配置替换，事务故障与单事件租约/replay另行验证；没有迁移或 API/Agent 变更，完整容量、更多竞争及发布集成继续保留。
