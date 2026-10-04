# HTTP service monitoring (SVC-01 through SVC-03)

Status: protocol design and implementation in progress; no released HTTP checks.
Roadmap authority: `MyProbe后续开发文档.md`, sections 6.2–6.4.

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
- Server-issued task ID, service ID, planned slot, deadline and configuration
  revision bind the result to an authorized, outstanding check. Reject duplicate,
  wrong-node, wrong-service and expired results without adding extra observations.

## Network and privacy enforcement

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
