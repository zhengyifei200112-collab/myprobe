# MyProbe

MyProbe is a self-hosted VPS monitoring platform with an original implementation,
a lightweight Go agent, and a responsive dashboard inspired by the information
architecture and visual polish of ZJM.

The product scope and acceptance evidence are tracked in
[`docs/PRODUCT_SPEC.md`](docs/PRODUCT_SPEC.md), and the versioned agent protocol is
defined in [`docs/PROTOCOL.md`](docs/PROTOCOL.md).

## Architecture

- Go server and single-binary Go agent
- Vue 3, TypeScript, Vite, and ECharts frontend
- SQLite in WAL mode with explicit migrations and history rollups
- Authenticated WebSocket reporting with HTTP fallback
- Embedded frontend assets in production releases

## Repository layout

```text
cmd/server                 server entry point
cmd/agent                  agent entry point
internal/protocol          versioned wire protocol
internal/store             SQLite schema and repositories
internal/agentgateway      agent WebSocket/HTTP ingestion
internal/httpapi           public and administrative HTTP API
internal/collector         host metric collection
web                        Vue application
docs                       product, protocol, and security specifications
deploy                     container and service deployment files
```

## Development status

The previous `myprobe-test` repository was an incomplete prototype and is not used as
the implementation base. This repository uses an independently documented protocol and
a testable vertical architecture.

The first vertical slice is operational: the server bootstraps SQLite and administrator
authentication, agents report real host metrics over authenticated WebSockets with an
HTTP fallback, typed Ping/TCPing tasks are scheduled and persisted, and the embedded
responsive dashboard updates metrics and latency in real time. Bounded historical APIs
and lazy-loaded charts cover the 1h/12h/1d/3d/7d/30d views without sending raw long-range
samples to the browser. Transactional retention keeps seven days of raw samples,
30 days of one-minute rollups, and one year of five-minute rollups. Monthly traffic accounting handles configurable reset days,
short months, host counter resets, and persisted O(1) dashboard reads. The responsive
management console covers login, node lifecycle and token rotation, Ping/TCPing target
CRUD, target groups, node assignments, encrypted notifications, alert rules, and
password-protected read-only chart sharing. Its maintenance area provides previewable,
versioned configuration transfer and passphrase-encrypted full database backups with
restart-safe staged restore and automatic preservation of the previous database. The
public cards also support persisted OS/platform labels, tested commercial expiry status,
theme-safe structured badges, validated external links, and server-sanitized advanced
HTML. The feature-by-feature implementation and evidence matrix is maintained in the
product specification.

## Docker deployment

```bash
cp .env.example .env
# Replace MYPROBE_ADMIN_PASSWORD and MYPROBE_ENCRYPTION_KEY in .env.
docker compose up -d --build
```

The default bind address is `127.0.0.1:25775`; publish it through an HTTPS reverse proxy
with WebSocket support. See [`deploy/README.md`](deploy/README.md) for proxy/security
guidance and hardened systemd units. The SQLite database is stored in the
`myprobe-data` volume.

## Local development

Requirements: Go 1.26+, Node.js 22+, and npm.

```bash
npm --prefix web ci
npm --prefix web run build
go test ./...
go run ./cmd/server
```

The production server embeds the output of the frontend build. During UI development,
run `npm --prefix web run dev`; Vite proxies API and WebSocket traffic to the server on
port `25775`.

Create a node through the authenticated admin API, then start its agent with the token
that is returned once:

```bash
go run ./cmd/agent --server http://127.0.0.1:25775 --token <agent-token>
```

Configuration is supplied through `MYPROBE_*` environment variables. Set
`MYPROBE_ADMIN_PASSWORD` before the first production startup; otherwise a random initial
password is printed once to the server log. Set `MYPROBE_ENCRYPTION_KEY` to a stable,
random value of at least 32 characters before configuring Webhook or Telegram
notifications. Back up that key separately from the SQLite database.
Forwarded client IP headers are ignored unless the reverse proxy address or CIDR is
explicitly listed in `MYPROBE_TRUSTED_PROXIES`.

History retention is configurable with `MYPROBE_RAW_RETENTION_DAYS`,
`MYPROBE_MINUTE_RETENTION_DAYS`, `MYPROBE_FIVE_MINUTE_RETENTION_DAYS`, and
`MYPROBE_RETENTION_INTERVAL_HOURS`. Durations must remain ordered from raw to
one-minute to five-minute storage.

## License

MIT
