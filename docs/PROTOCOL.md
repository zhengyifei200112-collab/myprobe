# Agent protocol v1

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

Acknowledges the latest accepted sequence. HTTP hello/report responses remain plain
acknowledgement objects and may additionally contain `config` with the current
collection/report intervals. Older agents ignore this optional field; newer agents
accept older responses without it, including empty HTTP success responses.

### `config`

Updates collection/report intervals, monitored interfaces and mount points.
Intervals are independently validated in the range 1–3600 seconds. An omitted or
invalid interval leaves its current value unchanged. Omitted interface/mount lists
preserve local Agent selections; explicit empty lists reset to automatic discovery.

Agents advertising `config.intervals.v1` apply both welcome and subsequent config
messages. They collect independently of reporting, retain only the latest successful
sample, and preserve its `captured_at` on repeat uploads. Intermediate samples are
not buffered for replay when collection is faster than reporting. Shortening an
interval restarts its timer immediately; an already running collection or upload
finishes within its existing timeout. Collection starts immediately on Agent startup.

The server refreshes desired WebSocket intervals on the next report or heartbeat
(the Agent heartbeat interval is 25 seconds). HTTP fallback receives them on the
next hello/report response. This is eventual application, not a synchronous Agent
acknowledgement of a management save. Cached or older samples refresh last contact
but do not add history, overwrite newer metrics, or modify traffic accounting.
Capture time remains the basis for determining data freshness. Agents released
before this capability must be upgraded to obtain independent interval behavior.

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
