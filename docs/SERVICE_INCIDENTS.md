# Service availability and durable incidents

Roadmap authority: `MyProbe后续开发文档.md`, sections 6 and 18.5.
Status: integration foundation; service incidents/reminders are not implemented yet.

The `feature/service-incidents` branch integrates HTTP checks from PR #53
(`d711950c2af8da9fe5147ee9d56259cfbb3c8e41`) with the durable incident platform
from PR #58 (`7bee226289bdd73e5e7c6b721ca618f5427b89e4`). Both source PRs remain
unmerged. The combined branch preserves their contracts and is not a release.
Reserved HTTP migrations 016–020 coexist with incident 015 and alert 021–033;
existing numbered migrations have not been rewritten.

## Integrated baseline

HTTP execution remains explicitly opt-in. Server startup preserves both HTTP
execution/capability negotiation and validated notification-origin configuration.
Management exposes both HTTP service and notification centers, preserving private
configuration, bounded statistics, deep-link login and configuration transfer.
Generated assets are rebuilt from the combined source rather than selected from
either source branch. Public-node payloads and the existing v1 capability contract
remain as documented by the HTTP and incident specifications.

Upgrade tests independently construct the HTTP-development schema and the
incident-development schema, then apply missing migrations. They verify original
HTTP configuration/task/result IDs, pending-result admission, incident identity,
aggregate frozen body/key/member history, current lease completion and channel wait.
Foreign-key checks and repeat migration pass. These fixtures are not acceptance of
every historical release binary or an owner-approved production rollout.

Real Agent/scheduler/Gateway tests cover policy denial as unobserved, allowed private
GET/HEAD, status/content failures, redirect limit, oversized response, timeout and
untrusted TLS. The allowed fixture uses an explicit Agent CIDR/port policy and the
production executor. HTTP browser CRUD/execution/reload/result flows pass in both
themes at 360/768/1440 px, alongside all policy, ACK, silence, maintenance, channel
wait, single-event-link and aggregation browser regressions. Local full Go has only
the known Windows collector path case (separate PR #47); vet/build/diff checks pass.
Local CGO is disabled, so no local race-detector evidence is claimed.

## Required next implementation

1. Persist service alert configuration and service/observer event identity. Reuse
   existing incident state and notification Outbox/attempts, including suppression,
   retries, restore and management fences. Do not create a second delivery queue.
   Service-bound execution rules must not be migrated into host-scope policies.
2. Evaluate fresh, admitted current-revision observations per selected observer.
   Missing, offline, unsupported, policy-denied or unexecuted checks are unknown;
   they must neither manufacture recovery nor become target failures. A host can
   remain online while an actual failed service observation creates a fault.
   Service summary must disclose its per-observer aggregation rule.
3. Persist 14/7/1-day certificate reminder deduplication by service, certificate
   identity and threshold. Cover replacement, repeated reports, exact boundaries
   and restart; certificate display alone does not prove reminder delivery.
4. Exclude the union of maintenance plan slots from service statistics while
   retaining original observations and separately reporting exclusions. Overlap
   must not double-subtract; notification suppression alone is insufficient.
5. Add protected service/observer/time-range navigation and diagnostics for DNS,
   TCP, TLS, HTTP status, assertions, missing execution and unsupported capability.
   Avoid raw URLs, bodies, internal addresses and credentials in notifications or
   public output. Verify real Agent failure → incident → local receiver → login
   navigation → recovery with restart/restore and concurrent configuration changes.

Release integration, data retention/capacity, historical Agent acceptance, public
provider/client rendering and owner merge approval remain separate gates. M1–M5
retain their original scope; this integration does not complete SVC-03 or M3.
