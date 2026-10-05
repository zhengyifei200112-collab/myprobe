# HTTP service monitoring (SVC-01 through SVC-03)

Status: protocol design and implementation in progress; no released HTTP checks.
Roadmap authority: `MyProbe后续开发文档.md`, sections 6.2–6.4.

Implemented so far: `internal/protocol/httpcheck.Spec` validates the basic request
shape, resource limits and assertion shapes with boundary tests. Task/result
binding validates IDs, configuration revision, planned slot and bounded times.
The v2 envelope, mutual-capability helper and Server WebSocket endpoint are
implemented, with Agent v2-first transport selection and bounded v1 fallback.
Agent execution/result transport is connected; Server ingestion and scheduling
remain pending. Syntax acceptance
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
The Server v2 welcome currently grants no HTTP extension because ingestion and
scheduling are not wired. Incoming HTTP results therefore reach the unsupported-type response, not an
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
  `Assertion.Matches` implements literal-key traversal and typed scalar equality.
  Numbers compare normalized decimal coefficients/exponents without float64
  rounding or exponent expansion: `1.00` equals `1e0`, while adjacent integers
  above 2^53 remain distinct. Missing fields do not equal null. The body must be
  UTF-8 and at most one MiB, contain one complete JSON value, use unique object
  keys, and stay within 64 nesting levels and 4096 bytes per numeric token.
  Arrays may occur in the response but are not traversable or scalar-equal.
  The executor must enforce any smaller configured body limit before matching.
- Server-issued task ID, service ID, planned slot, deadline and configuration
  revision bind the result to an authorized, outstanding check. Reject duplicate,
  wrong-node, wrong-service and expired results without adding extra observations.

## Network and privacy enforcement

Task IDs and service IDs are 1–128 UTF-8 bytes without whitespace/control
characters; revision is nonzero. A received task must still be unexpired and its
deadline at most ten minutes ahead. The planned slot may be at most ten minutes
old or one minute ahead to allow bounded clock skew. Results repeat the original
task/service/revision/slot and complete within its slot/deadline window with up to
one second of cancellation-finalization tolerance. Receipt
grace is one minute after expiry; it does not extend execution time. Elapsed
duration must be finite, nonnegative and within the timeout plus one second of
cleanup allowance. Network work still uses the original deadline; elapsed time
is measured rather than clamped. Ingestion must
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

The initial `httpprobe.Policy` implements this address/port decision, with tests
for mapped IPv4, private CIDR boundaries, forbidden ranges and local overrides.
Private allowances must be contained in RFC1918 or ULA space; broad public,
loopback or link-local allowances are rejected. Special address exclusions take
precedence even over an allowed private CIDR. IPv6 outside global unicast
allocation space is denied by default. A guarded dialer resolves once, validates
all answers before dialing, rejects mixed permitted/forbidden answers, and passes
only validated literal IPs to the underlying TCP dialer. A new dial repeats DNS
validation. At most 64 addresses are accepted. Tests use injected resolution and
in-memory connections, not live public targets. HTTP transport now uses this
dialer with proxies disabled and no connection reuse across redirect hops. Each
redirect URL is revalidated, and HTTPS downgrades are denied. Execution uses four
slots, one total deadline, a 64 KiB header limit and the decoded-body limit before
assertions. TLS uses normal trust verification and TLS 1.2 minimum. Local HTTP
tests cover GET/HEAD, status failures, oversized bodies, redirects, loops, private
redirect rejection and timeout. Local TLS tests additionally cover trusted,
untrusted, expired and hostname-mismatched certificates, certificate evidence and
HTTPS downgrade rejection. A private test trust pool trusts only generated fixture
certificates; production continues using system trust without a verification bypass.
Compressed oversized bodies are limited after decoding. Four live blocked requests
verify busy rejection, cancellation and release of all execution slots. The Agent
now advertises its HTTP executor on v2, executes only after a welcome grants the
same capability, and bounds task goroutines to four independently of latency
tasks. Work is cancelled with its connection; replies use the original socket,
never a replacement session or the metrics HTTP fallback. A real v2 session test
verifies a negotiated loopback task returns policy-denied without dialing it.
Local CIDRs/ports are accepted by Client configuration and Agent flags/environment
variables described below. Server result persistence remains pending.

### Agent-local deployment configuration

`-http-private-cidrs` (default: `MYPROBE_HTTP_PRIVATE_CIDRS`) accepts up to 64
comma-separated RFC1918/ULA CIDRs. Empty means no private targets.
`-http-additional-ports` (default: `MYPROBE_HTTP_ADDITIONAL_PORTS`) accepts up to
64 comma-separated decimal ports from 1 through 65535, in addition to 80/443.
Explicit flags override environment values, including an explicitly empty flag.
Whitespace around entries is trimmed; empty entries, malformed ports and invalid
CIDRs fail startup with exit code 2. Errors do not echo configured values.

For example, `-http-private-cidrs=10.2.0.0/16 -http-additional-ports=8080` permits
HTTP probes into that private subnet and adds port 8080. Address and port lists
are independent: added ports also apply to otherwise permitted public targets.
There is no per-subnet port mapping. Loopback, link-local and explicitly blocked
metadata addresses remain denied. Restart the Agent after changing local policy.
These options do not enable Server scheduling: the current Server still grants
no HTTP capability until dispatch and result persistence are implemented.

Classify DNS, refused connection, timeout, TLS failure, expired certificate,
status mismatch, content mismatch, oversized response and internal execution
failure without returning raw library errors. Return no body, credentials,
resolved addresses, redirect URLs or response headers. Certificate evidence is
bounded and excludes arbitrary certificate subject strings.

## Required evidence before release

### Configuration transfer v2

Configuration export now emits version 2 with an optional `http_services` array
containing IDs, editable specs and node assignments. Version 1 imports remain
supported, but v1 documents containing HTTP service fields are rejected. Transfers
exclude task results, historical revisions, certificates and Agent-local network
allowances. Exported URLs and assertions are private configuration; treat this
administrator download as sensitive deployment data rather than a diagnostic file.

Import merges by service ID within the existing all-or-nothing transaction, including
nodes and other configuration. Imported service assignments replace that service's
current assignments; omitted services remain unchanged. Existing services advance
their revision and start new schedule epochs, invalidating old tasks. Import preview
reports `http_services_created` and `http_services_updated` without committing rows
or returning generated Agent tokens. Exports and imports cap HTTP services at 1,000.
An unassigned service left by node deletion can round-trip with no assignments; it
cannot dispatch until nodes are selected. Normal create/edit APIs still require
at least one node. The maintenance UI still needs to present these new preview
counts before this transfer work is considered complete end-to-end.

### Administrator configuration API

`POST /api/v1/admin/service-monitors` creates a service, `GET` on
`/api/v1/admin/service-monitors/:serviceID` reads its private configuration, and
`PUT` on that resource replaces the complete editable configuration with an
expected positive `revision`. Creation accepts only revision zero (or omitted).
All editable fields are required, including an explicit boolean `enabled`.
Requests are limited to 64 KiB, reject unknown fields/trailing JSON, and use the
existing administrator session and CSRF enforcement. Every response, including
unauthenticated rejection, carries no-store caching policy.

Responses wrap `service` and the gateway's `execution_enabled` flag. Saving an
enabled configuration does not itself enable production dispatch. A stale revision
returns 409; malformed configuration or nonexistent selected nodes returns 400;
missing reads return 404. Internal storage errors are generic 500 responses.
Audit records include only revision, enabled state and selected-node count, never
target URLs or assertion contents. UI and configuration transfer remain pending;
no public service configuration route is provided.

`GET /api/v1/admin/service-monitors?limit=50&after=ID` lists summaries ordered by
stable service ID. Limits are 1–100 and the response includes `next_cursor` (empty
at the end). Summaries contain name, revision, enabled state, interval, node count
and update time, but omit URLs and assertions. Pagination is not a cross-request
snapshot: concurrent additions before the cursor appear after a fresh list reload.

`DELETE /api/v1/admin/service-monitors/:serviceID?revision=N` requires a positive
expected revision and CSRF. Success returns 204 and removes assignments, tasks and
schedule history through foreign keys. A stale revision or already deleted service
returns 409, requiring refresh; this prevents deleting a concurrently changed
configuration. The UI must disclose history deletion before submitting the action.

### Raw HTTP history retention

Migration `019_http_retention.sql` adds a durable retention floor and a task-time
index. Existing history maintenance now removes HTTP tasks older than 31 days and
closed schedule epochs ending before that boundary, up to 10,000 rows of each per
maintenance pass. Open epochs keep their original anchors. Large backlogs drain
over successive passes; the retention floor is advanced in the same transaction
and never moves backwards, even after a clock correction. HTTP currently has no
long-term rollup; this 31-day policy is separate from host/latency retention.

Statistics return requested start/end, effective start/end and retained-from time.
The effective start is clamped to retained history, so deleted samples do not become
missing observations. A wholly expired range has zero counts and null rates, with
an empty effective interval. Raw timestamp deletion uses a whole-second boundary
that preserves samples exactly on that boundary. The current tests cover partial
range trimming, deletion and a non-regressing floor after clock rollback.

Capacity testing must verify that bounded cleanup keeps pace with ingestion at the
supported deployment size. Earlier-than-retained queries are not evidence of past
availability; future UI must visibly show the actual range and history limitation.

### Per-observer statistics foundation

The internal statistics query reads schedule epochs and task results in one read
transaction. It returns expected, success, failure, unobserved and missing counts;
missing equals expected minus success minus failure, so unobserved is a subset of
missing, not an additional denominator. Success rate is success/(success+failure),
coverage is (success+failure)/expected, and empty denominators produce null. Rates
are fractions from 0 to 1. Results outside a known revision's exact planned slots
do not contribute to the numerator.

The effective end is capped at observation time minus 126 seconds to conservatively
exclude slots that may still produce a valid result (dispatch window, maximum
timeout, receipt grace and cleanup). A wholly immature query returns an empty
effective interval, zero counts and null rates. Both bounds are half-open and
nanosecond timestamps are compared in Go after widened indexed text bounds.
Queries reject more than 31 days, 10,000 revisions or 100,000 candidate results.

This primitive is not exposed as a public availability endpoint yet. Maintenance
exclusions, pre-migration unknown coverage, retained-history boundaries and the
requested-versus-effective range presentation remain required before API/UI use.

### Expected-slot history

Migration `018_http_schedule_epochs.sql` records enabled service/node revisions
with nanosecond schedule anchors and exclusive end times. Saving configuration
closes prior epochs and creates enabled replacements in the same transaction as
the configuration and assignment changes. Disabling stops future expected slots;
it does not erase earlier expectations. The internal count primitive uses
`[start,end)` and counts slots even when no task was dispatched. It bounds queries
to 31 days and 10,000 overlapping revisions per service/node, rejecting excessive
ranges rather than silently truncating results. Caller-supplied observation time
must cap historical queries; maintenance exclusions are not implemented yet.

On upgrade from migration 017, a transactional Go backfill preserves the exact
current revision's timestamp without SQLite fractional-second rounding. Older
configuration revisions cannot be reconstructed and must be disclosed as unknown
coverage in future statistics APIs. Deleted services/nodes remove their epochs
through foreign keys, matching task-history deletion. This history is a prerequisite
for statistics, not a complete availability API; result counts, maturity windows,
maintenance and retention boundaries still need integration.

### Periodic scheduling foundation

The HTTP scheduler reads enabled service/node assignments without loading private
URLs. Planned slots are anchored to the configuration revision's update time and
repeat at the configured interval. Only slots within a five-second dispatch window
are eligible; downtime does not cause historical checks to be replayed. Each slot
is attempted once per process, including offline/unsupported/busy outcomes, with
the durable task uniqueness constraint preventing duplicate sends after restart.
Four workers bound concurrent dispatch and each write has a deadline. Configuration
updates reset the schedule anchor; task creation rejects slots predating that
revision, and removed assignments are removed from the scheduler's memory.

This scheduler is implemented and tested but not yet started by production main.
Expected-slot accounting must retain configuration history so offline or missed
slots can be shown as missing even when no task row exists. Counting only stored
tasks as expected checks would overstate coverage and is not an acceptable
statistics implementation. Retention and queries remain part of this feature's
release gate.

### Configuration storage foundation

Migration `016_http_services.sql` adds private service configuration and explicit
node assignments. Service intervals are 30–86400 seconds and must exceed the
request timeout; each save requires 1–100 distinct existing nodes. Configuration
updates compare the caller's revision and increment it atomically with assignment
replacement. Failed assignments roll back the complete change. Node deletion
cascades assignments; a service left without nodes must not be dispatched.
URLs remain private configuration and must not be serialized into public views.

The migration number must be reconciled with other unpublished branches before
merge (batch management and incidents have separate migrations). Existing tables
and data are unchanged. Database snapshots include the new tables; configuration
export/import support, service APIs, scheduling, transport ingestion and retention
remain to be implemented before enabling this capability. No downgrade procedure
for the new schema is claimed; use a pre-upgrade backup when reverting.

### Durable tasks and result admission

Migration `017_http_tasks.sql` persists the server's original task before dispatch,
including its node, revision and planned slot. A unique service/node/revision/slot
constraint prevents scheduling the same slot twice. Task creation atomically checks
that the service is enabled and the node remains assigned at the expected revision.

The storage ingestion method requires an authenticated node ID supplied by the
transport. It validates the result against the saved original task, including its
time window, then conditionally records only the first valid result while checking
current configuration and assignment again. Wrong-node, late, changed-revision and
duplicate reports are rejected. Pending records survive Server restart. No result
is treated as success when absent; unobserved outcomes remain explicitly distinct.
Gateway v2 ingestion now calls this storage method only for a task sent on the
same current authenticated session. An integration gate, off by default, grants
HTTP capability on new v2 handshakes. Dispatch persists the original task before
sending and allows at most four outstanding tasks per session; expired entries
are pruned after the receipt grace. Failed writes leave an unobserved durable slot,
not a fabricated service failure. Acknowledgements follow successful storage only.
Duplicate, unsolicited and replacement-session results are rejected. Existing v1
sessions never receive HTTP tasks. Production startup does not enable the gate yet.
Retention, expected-slot accounting, restart dispatch policy and statistical queries
remain required before enabling the feature. Deleting a service or node cascades
its task records, so historical views must explain that deletion removes that data.

Use local controllable HTTP/TLS servers and injected resolvers/dialers. Cover 200,
500, GET/HEAD, slow headers/body, redirect loops and address-policy transitions,
DNS rebinding, overlarge decoded bodies, invalid/expired certificates, cancellation,
duplicate/out-of-order results and old-Agent capability behavior. Statistics must
keep success, failure, expected slots, missing slots and maintenance exclusions
distinct; an empty denominator is null. Full implementation needs API/protocol,
forward-only schema, configuration export, backup/restore, docs and UI acceptance.
