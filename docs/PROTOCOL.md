# Agent protocol v1

The v1 runtime contract below remains unchanged. An opt-in v2 HTTP-check contract
is being developed in [HTTP service monitoring](HTTP_SERVICE_MONITORING.md).
The Server offers `/api/v2/agent/ws` with explicit v2 envelopes and a capabilities
array in its welcome. HTTP capability requires `MYPROBE_HTTP_PROBES_ENABLED=true`
on the Server and explicit Agent support; do not send HTTP tasks
to v1 Agents or assume that a v2 connection establishes HTTP execution support.

The independent `config_apply.v1` optional v2 capability manages sampling periods
only. Mutually negotiated peers receive `welcome.runtime_config` and live `config`,
then acknowledge actual application with `config_applied`. Revision, connection
generation, nonce and exact values fence current proof. Unnegotiated v1/v2 peers
receive no new frames. See [Agent runtime evidence](AGENT_RUNTIME_EVIDENCE.md) for
the payload, independent collection/report semantics and released-Agent checks.

Private service diagnostics use already admitted HTTP results, not a new Agent
request. Failure categories identify available evidence; the Agent still reports
only total duration, not DNS/TCP/TLS phase timing or resolved addresses. See
[service diagnostics](SERVICE_DIAGNOSTICS.md) for the protected history/navigation
contract and Server-only migration 036. No new public or Agent fields are required.

The independent `network_probe.v1` v2 capability negotiates `network_task` and
`network_result` for typed Ping/TCP probes. The current Server enables this after
schema initialization; both Hello and Welcome must explicitly agree. Requested
family and packet limits are immutable task inputs, while result family/counts
are actual optional evidence. No resolved address or tool output is returned.
v1 remains unchanged; unsupported advanced profiles are recorded as a Server
dispatch decision without executing in a different family. Result admission is
fenced by the current authenticated connection generation; ACK follows the
atomic receipt/sample commit. The Agent currently has no new durable result retry
queue. See [network evidence](NETWORK_PROBE_EVIDENCE.md) for exact contracts,
timeouts, packet semantics, idempotency and native test limitations.

## Transport

- Primary: `GET /api/v1/agent/ws` using WebSocket.
- Fallback: `POST /api/v1/agent/report`.
- Authentication: `Authorization: Bearer <agent-token>`.
- Production deployments must use HTTPS/WSS.
- JSON messages use UTF-8 and have a maximum accepted size of 256 KiB.

## Envelope

```json
{
  "version": 1,
  "type": "report",
  "sequence": 42,
  "sent_at": "2026-07-20T12:00:00Z",
  "payload": {}
}
```

`sequence` is monotonically increasing for the life of an agent process. The server
uses it for diagnostics and acknowledges the latest accepted sequence.

## Agent to server messages

### `hello`

Identifies the agent version, host identity, supported capabilities, operating system,
architecture, and collection interval.

### `report`

Carries absolute resource values and network counters plus elapsed-time-derived rates.
Percentages are included for display convenience but never replace absolute values.

### `ping_result` and `tcping_result`

Return the task ID, target ID, latency, success state, error class, and completion time.

### `heartbeat`

Keeps an otherwise idle connection alive. Regular reports also count as heartbeats.

## Server to agent messages

### `welcome`

Confirms authentication, server time, reporting interval, and connection ID.

### `ack`

Acknowledges the latest accepted sequence.

### `config`

Updates collection/report intervals, monitored interfaces and mount points.

### `task`

Schedules a typed Ping or TCPing operation. Arbitrary commands are not supported.
Tasks carry a short expiry, strict timeout, validated host, and (for TCPing) a valid port.
The agent caps concurrent probes and invokes the operating system's fixed `ping` program
without a shell; TCPing uses a direct socket connection.

## Connection lifecycle

1. Agent connects with a bearer token and sends `hello` within five seconds.
2. Server replies with `welcome` and the effective configuration.
3. Agent sends reports and reads tasks concurrently.
4. Both peers maintain read deadlines and Ping/Pong heartbeats.
5. Agent reconnects using exponential backoff with jitter, capped at one minute.
6. When WebSocket is unavailable, reports use HTTP without disabling reconnect attempts.

## Compatibility

- Unknown fields must be ignored.
- Unknown message types return a protocol error without terminating a healthy connection.
- Breaking changes require a new versioned endpoint and envelope version.
