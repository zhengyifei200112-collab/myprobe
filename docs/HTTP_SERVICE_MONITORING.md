# HTTP service monitoring (SVC-01 through SVC-03)

Status: protocol design and implementation in progress; no released HTTP checks.
Roadmap authority: `MyProbe后续开发文档.md`, sections 6.2–6.4.

Implemented so far: `internal/protocol/httpcheck.Spec` validates the basic request
shape, resource limits and assertion shapes with boundary tests. Task/result
binding validates IDs, configuration revision, planned slot and bounded times.
The v2 envelope, mutual-capability helper and Server WebSocket endpoint are
implemented, with Agent v2-first transport selection and bounded v1 fallback.
HTTP runtime integration remains pending. Syntax acceptance
does not allow a network request or waive the Agent address policy below.

## User outcome and delivery boundaries

An administrator assigns GET/HEAD service checks to selected Agents and sees
whether each observation point can reach the service, satisfy status/content
assertions and validate HTTPS. A running host does not establish service health.
Missing checks remain unknown and coverage is reported separately from success.

SVC-01 defines bounded task/result contracts and capability negotiation. SVC-02
adds the execution policy, scheduling, persistence and idempotent result ingestion.
SVC-03 supplies configuration, results, certificate reminders and alert integration.
Protocol declarations alone do not establish support in a running Agent.

## Compatibility decision

Existing v1 rejects unknown envelope types and accepts only ping/tcping tasks.
Do not send HTTP tasks over an unnegotiated v1 connection. Preserve existing v1
endpoint/report handling; HTTP support requires an explicit versioned extension
with negotiated `http_probe.v1` capability. Before enabling scheduling, implement
and test new Agent/new Server, old Agent/new Server and new Agent/old Server paths.
The exact connection negotiation is an implementation gate, not assumed complete.
An old Agent is unsupported, never a failed service observation. Do not advertise
the capability until the executor and result transport are operational.

`internal/protocol/v2` retains the outer JSON shape with `version: 2`, adding
`http_task` and `http_result`. The v2 welcome contains a `capabilities` array
computed from explicit Agent/Server support. Unknown extensions are excluded;
only the exact `http_probe.v1` capability permits HTTP checks. Legacy message
payload shapes can be reused within a v2 envelope, but a receiver must never
silently reinterpret a version-1 envelope as version 2 or vice versa. The outer
validator does not replace payload, session or capability validation.

Server exposes `/api/v2/agent/ws` alongside the unchanged v1 endpoint. Both use
the existing bearer-token authentication and hello validation. Each connection
requires its endpoint's envelope version, including subsequent heartbeat/report
frames. Responses and latency task dispatch use that same version. A node still
has one active session; a newer authenticated connection replaces the older one.
The v2 welcome currently grants no HTTP extension because execution/ingestion are
not wired. HTTP messages therefore reach the unsupported-type response, not an
execution path. Integration tests exercise real loopback v1/v2 handshakes,
version-correct heartbeat acknowledgements and cross-version rejection.

The Agent first dials the v2 path and retries v1 only on HTTP 404 or 405. It does
not downgrade on authentication denial, network failure, server error, unexpected
HTML or an invalid welcome. Reconnect attempts probe v2 again, allowing a Server
upgrade to take effect. Socket reports, heartbeat and latency results retain the
connection's selected version. HTTP hello/report fallback remains v1 and does not
inherit the failed WebSocket frame's version. Every received socket frame is
version-validated before processing. Full Agent/Server end-to-end compatibility
with historical release binaries and HTTP execution/result ingestion still
require further acceptance evidence. A real Agent/Store/Gateway integration test
now covers new Agent with v2 and v1-only handlers, persists a synthetic metric
report over each socket version, verifies retained hello metadata and sends a
successful v1 HTTP report after disconnect. This exercises the legacy handler
contract rather than claiming validation of every historical binary release.

## Bounded check contract

- GET or HEAD only. Absolute HTTP/HTTPS URL, at most 2048 bytes, no userinfo,
  fragment, control characters or opaque URL. Credentials and custom headers are
  absent from the contract; URLs must not carry secrets. Public views and errors
  never expose the full URL or response body.
- Explicit status allowlist of 1–32 unique codes from 100 through 599.
- Total timeout 100–60000 ms; schedule interval at least 30 seconds and strictly
  greater than timeout. Initial UI default: five-second timeout.
- Zero to three redirects. Maximum decoded response body one MiB. Execution
  concurrency defaults to four HTTP checks per Agent, separate from latency work.
- Optional UTF-8 text containment or restricted JSON field equality. At most one
  assertion, with bounded path/value lengths. HEAD cannot use body assertions.
  JSON field access is data traversal, never an expression or script evaluator.
- `assertion.kind` is `text_contains` or `json_equals`. Text is 1–4096 UTF-8
  bytes. JSON paths contain 1–16 literal object keys, each 1–128 UTF-8 bytes
  without control characters. Dots/brackets inside a key are literal, not path
  operators; arrays and wildcards are not supported. `expected` is exactly one
  JSON scalar of at most 4096 bytes, preserving numeric text; absent and explicit
  null differ. Object/array expected values and mixed assertion fields are invalid.
  Execution and numeric equality semantics remain an executor implementation gate.
- Server-issued task ID, service ID, planned slot, deadline and configuration
  revision bind the result to an authorized, outstanding check. Reject duplicate,
  wrong-node, wrong-service and expired results without adding extra observations.

## Network and privacy enforcement

Task IDs and service IDs are 1–128 UTF-8 bytes without whitespace/control
characters; revision is nonzero. A received task must still be unexpired and its
deadline at most ten minutes ahead. The planned slot may be at most ten minutes
old or one minute ahead to allow bounded clock skew. Results repeat the original
task/service/revision/slot and complete within its slot/deadline window. Receipt
grace is one minute after expiry; it does not extend execution time. Elapsed
duration must be finite, nonnegative and within the task timeout. Ingestion must
match the authenticated node and atomically consume the Server-stored task;
the stateless validator cannot detect replay or authorize a different node.

Results use `success`, `failure`, or `unobserved`. Unsupported capability, busy
executor, cancellation, denied local policy, invalid task and internal executor
errors are unobserved and excluded from service success/failure counts. A success
requires an allowed HTTP status and no error class. Response status/content
mismatches require an HTTP status. No arbitrary error message is transported.

`certificates` contains at most `max_redirects + 1` leaf-certificate observations
in TLS handshake order. Each includes a 32-byte SHA-256 fingerprint encoded as
hex, a nonempty validity interval and verification flag. Subjects, SANs and raw
certificates are excluded. An HTTPS success requires certificate evidence and
every supplied certificate must be verified; certificates from HTTP-to-HTTPS
redirects may also be supplied. Executor TLS verification is still mandatory:
this validator checks result consistency, not certificate trust. Status mismatch
must use a disallowed status; content mismatch requires an assertion and an
allowed status, establishing status-before-content failure precedence.

Task syntax validation cannot prove a target is safe to dial. The Agent executor
must validate every resolved address, dial only the validated address, preserve
the original HTTP host/TLS server name, and repeat checks at every redirect.
Default policy denies loopback, private, link-local, multicast, unspecified and
metadata ranges, including IPv4-mapped IPv6. Default ports are 80/443. Additional
ports and private CIDRs require explicit local Agent configuration; no task field
can relax this policy. Do not use ambient proxy settings or skip TLS validation.

Classify DNS, refused connection, timeout, TLS failure, expired certificate,
status mismatch, content mismatch, oversized response and internal execution
failure without returning raw library errors. Return no body, credentials,
resolved addresses, redirect URLs or response headers. Certificate evidence is
bounded and excludes arbitrary certificate subject strings.

## Required evidence before release

Use local controllable HTTP/TLS servers and injected resolvers/dialers. Cover 200,
500, GET/HEAD, slow headers/body, redirect loops and address-policy transitions,
DNS rebinding, overlarge decoded bodies, invalid/expired certificates, cancellation,
duplicate/out-of-order results and old-Agent capability behavior. Statistics must
keep success, failure, expected slots, missing slots and maintenance exclusions
distinct; an empty denominator is null. Full implementation needs API/protocol,
forward-only schema, configuration export, backup/restore, docs and UI acceptance.
