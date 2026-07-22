# MyProbe

MyProbe is a self-hosted VPS monitoring platform with an original implementation,
a lightweight Go agent, and a responsive dashboard inspired by the information
architecture and visual polish of ZJM.

The project is currently under active development. The product scope is tracked in
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
the implementation base. This repository starts from a documented protocol and a
testable vertical architecture.

The first vertical slice is operational: the server bootstraps SQLite and administrator
authentication, agents report real host metrics over authenticated WebSockets with an
HTTP fallback, typed Ping/TCPing tasks are scheduled and persisted, and the embedded
responsive dashboard updates metrics and latency in real time. Bounded historical APIs
and lazy-loaded charts cover the 1h/12h/1d/3d/7d/30d views without sending raw long-range
samples to the browser. Monthly traffic accounting handles configurable reset days,
short months, host counter resets, and persisted O(1) dashboard reads. The responsive
management console covers login, node lifecycle and token rotation, Ping/TCPing target
CRUD, target groups, and node assignments. The remaining ZJM parity work is tracked in
the product specification.

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

## License

MIT
