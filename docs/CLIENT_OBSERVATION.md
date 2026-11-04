# Private Agent message observation

The test-only callback in internal/agentclient records allocated envelope sequences,
local rejections and transport returns for hello, heartbeat, report, latency,
HTTP-check, configuration-applied and reliable network-result messages. Install it
before starting the client and do not replace it while the client runs. Callbacks
can run concurrently and must provide their own synchronization; callback order
across goroutines is not sequence order. Keep callbacks bounded and nonblocking.
Production clients leave the callback nil and do not compute report digests.

A report observation contains its capture timestamp and payload SHA-256 for exact
correlation. Events contain no raw payload, token, node name, address, response
body or raw transport error. Transport failures use fixed error classes.
A successful WebSocket write or HTTP response is not evidence of application
acceptance or database commit. HTTP status is recorded independently of transport
errors; server receipts and persisted accounting remain separate evidence.

Fallback keeps the original report sequence and payload; it still applies the
existing HTTP sampling configuration. Configuration acknowledgements retain their
connection fence. The bounded reliable network-result queue and its ACK/retry
behavior are unchanged. The wire protocol and public API are unchanged.

Focused loopback tests cover cancellation, encoding rejection, disconnected
results, concurrent sequence allocation, privacy, HTTP status, real WebSocket to
HTTP fallback, sampling application, stale configuration fencing, legacy hello
and heartbeat, and v1/v2 wire envelopes. CI also runs them under the race detector.
These hooks do not constitute the complete joint-load observer or prove capacity.
The original 5-to-20-node and same-source 20-node/24-hour acceptance still applies.
