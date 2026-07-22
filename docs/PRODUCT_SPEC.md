# MyProbe product specification

## Product principles

1. Match the complete observable ZJM feature set without copying its source, assets,
   branding, or implementation details.
2. Keep the agent small, outbound-only, and safe by default.
3. Make every public metric explainable from a typed protocol field.
4. Preserve a one-command deployment and a single server binary release.
5. Treat offline state, traffic accounting, and notifications as correctness-critical.

## Feature parity matrix

| Area | Requirement | Acceptance evidence | Status |
| --- | --- | --- | --- |
| Public overview | Current time, online/offline counts, aggregate traffic and rate | Responsive browser tests and live API data | Implemented |
| Public filtering | Tag/region pills with counts and horizontal mobile scrolling | UI test at 360/768/1440 px | Implemented |
| Node cards | Flag, name, status, masked IP, OS, uptime, last update | Metadata persistence, privacy and API contract tests | Implemented |
| Capacity | CPU model/cores, memory and disk totals and utilization | Agent integration test | Implemented |
| Network | Current up/down rate, lifetime and billing-cycle traffic | Counter-reset and interval tests | Implemented |
| Commercial metadata | Price, billing period, expiry date and remaining days | Exact, leap-day and past/future boundary tests | Implemented |
| Latency | Ping/TCPing targets, groups, display mode and current results | Scheduled task integration test | Implemented |
| Theme | Light, dark, and system preference | Browser theme tests | Implemented |
| Realtime | WebSocket updates, reconnect, cached last-known data | Disconnect/recovery test | Implemented |
| Charts | Ping, TCPing, upload, download, and total traffic history | 1h/12h/1d/3d/7d/30d queries | Implemented |
| Chart sharing | Group-scoped password protected chart views | Authentication and rate-limit tests | Implemented |
| Node administration | Register, edit, order, hide, delete, rotate token | Admin API and browser tests | Implemented |
| Target administration | Ping/TCPing target and group CRUD | API and scheduler tests | Implemented |
| Notifications | Telegram bot and generic webhook channels | Mock receiver tests | Implemented |
| Alerts | Offline/recovery, CPU, bandwidth, cycle traffic and expiry | Deduplication/cooldown tests | Implemented |
| Custom display | Structured badges/links and sanitized advanced HTML | Sanitizer, persistence and CSP tests | Implemented |
| Configuration | Versioned merge import/export and encrypted database backup/restore | Round-trip, tamper, dry-run and recovery tests | Implemented |
| Authentication | Password login/logout/change, CSRF, persistent throttling and CAPTCHA | Security integration and restart tests | Implemented |
| Audit | Administrative action log and cursor-paginated viewer | API and management UI verification | Implemented |

## Public dashboard layout

- Header: product brand, theme control, and admin entry.
- Overview: four cards on desktop and a 2x2 grid on mobile.
- Filter bar: `All`, dynamic tag groups, and `Other`.
- Node grid: one column below 900 px, two columns from 900 px, and three columns
  from 1250 px.
- Node cards use soft elevated surfaces, restrained gradients, rounded corners,
  tabular numeric values, and accessible warning colors.
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
- Public/local IP data is stored separately; public responses expose only masked values.

## Retention

- Latest state: in memory and persisted per node.
- Raw samples: seven days by default.
- One-minute rollups: 30 days.
- Five-minute rollups: one year.
- Retention and rollup jobs are configurable and transactional.

## Explicit exclusions for v1

Remote shell, arbitrary command execution, and terminal proxying are intentionally not
part of ZJM parity and are excluded from v1 to keep the agent attack surface small.
