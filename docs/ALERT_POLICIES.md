# Scoped alert policies (ALT-02)

Status: implementation in progress. The resolver, transactional storage, management
APIs/UI and runtime evaluation are integrated on this branch. Each evaluation tick
atomically maps legacy rules and synchronizes selected policies before reconciling
incidents. This branch started from main and now
includes incident/outbox dependency commit `99d08aa` from PR #50 for integration.
The GitHub PR remains unmerged; review this policy branch against that dependency.
Release gates still include extended live API/browser coverage, configuration transfer,
complete upgrade/restore coverage and measured capacity. This is not released.

## Contract

- A policy has a stable ID and `policy_key`, an enabled flag, integer priority
  (-1000 through 1000) and one scope: all, explicit nodes or positive tag selectors.
- Explicit scope supports 1–100 distinct node IDs. Tags support 1–64 distinct
  case-sensitive exact strings and require explicit `all` or `any` matching.
  Values are UTF-8, control-free, at most 128 bytes and contain no surrounding
  whitespace. Reject conflicting selector fields and empty selectors.
- Node scopes outrank tag scopes, which outrank global scope regardless of numeric
  priority. Within a scope tier, larger priority wins. Different keys coexist.
- Enabled policies with the same key, tier and priority cannot overlap. Explicit
  node scopes overlap when their ID sets intersect. Positive tag scopes always
  potentially overlap: future nodes can carry both tag sets. Reject such ties at
  save time rather than permitting future tag changes to choose a winner silently.
- Disabled drafts do not participate in conflicts or selection. Re-enabling must
  revalidate against the complete enabled set in the same write transaction.
- Resolve from current node tags so new qualifying nodes inherit policies. Return
  candidates in precedence order with the winner and a machine-readable reason:
  `only_match`, `more_specific_scope`, or `higher_priority`. Sort decisions by key.

The resolver validates up to 1000 policies and rejects ambiguous input rather than
using input order or a random ID as a tiebreaker. It does not mutate caller slices.
Administrator previews and evaluation must use this same resolution contract.

## Remaining implementation and release gates

1. Route future administrative writes through `alerts.Service.SavePolicy`, which
   rejects unknown configuration fields, malformed/trailing JSON, invalid thresholds
   and missing templates, and reuses legacy rule defaults. The Store separately
   validates object shape/size, known kinds, cooldown, scope and references.
2. Migrate legacy single-node rules to equivalent explicit policies. Preserve stable
   incident identities, active state, original thresholds/channels and pending delivery;
   a migration must not trigger mass notifications. Decide and test the adapter before
   changing evaluation authority.
3. Integrate selected policies into the existing incident evaluator and outbox, with
   management closure reasons when disabled or superseded. Unknown observations must
   continue to prevent false recovery.
4. Add authenticated CRUD and per-node effective-policy previews, showing inheritance,
   overrides and independent overlapping keys. Mutations require CSRF and safe audit
   metadata. Add bounded list/query behavior and actionable conflict errors.
5. Add UI, versioned configuration transfer, backup/restore and upgrade coverage.
   Keep product/protocol documentation and generated web assets synchronized.
6. Verify dynamic tag changes, future-node matching, competing saves, disable/re-enable,
   runtime restart and old-state migration. Maintenance and aggregation remain separate
   ALT-03/ALT-05 work and must not be claimed as delivered by this resolver.

## Persistence and concurrency

Migration 021 adds policy definitions and a singleton writer-reservation row.
Numbering leaves 014–020 for other unpublished feature branches; reconcile the
complete upgrade chain before merging. No existing rule or event state is changed.
Policies retain their own revision, timestamps, channel reference and scope.
Explicit node IDs are validated at save time; deleted nodes subsequently stop
matching without deleting the policy. A missing selected node must be removed from
the configuration before saving it again. Channel deletion is restricted while a
policy references it. The channel deletion API returns 409 with an actionable
message to update/delete the referencing policies, rather than leaking a SQLite
constraint error. Disabled policy references also protect the channel. Deletion
and concurrent policy creation are serialized by the database writer reservation.

Save acquires SQLite's writer reservation before reading the policy set, checking
revisions/references and validating conflicts. This serializes concurrent writers
across Store instances, rather than relying on a process-local mutex. A successful
save increments the revision. Stale updates, stale deletes and conflicting
activations are rejected. Failed writes roll back entirely. Listing is bounded
to 1000 definitions; attempts to create beyond the limit fail explicitly.

Effective-policy preview reads node tags and policies in one read transaction.
It returns current resolver decisions, not proof that a just-saved revision has
already reached the next evaluation tick. Configuration transfer still requires
integration. Database backups include the new tables, but production
restore/upgrade acceptance must be performed after all feature migrations converge.

`TestBackupRestoresPolicyBindingsWithoutDuplicatingFaults` exercises the actual
consistent SQLite backup, staged restore, pending-restore application and reopen
path for both legacy and materialized bindings. It changes/disables the policy
after backup, then verifies restoration of the original policy revision/config,
binding origin, execution-rule identity/timestamp, active incident and pending
delivery payload. Preparation and observation after restore neither remap the
rule nor duplicate the fault/job; the restored job remains claimable. This covers
database state, not encrypted archive transport, original channel-key availability,
cross-release migration or live notification delivery after restore.

Tests exercise cross-connection conflicting saves, stale edits/deletes, disabled
draft activation, invalid node references, channel deletion restrictions, dynamic
tag preview and reopen persistence. Endpoint authorization and synthetic browser
tests cover management; the basic live browser lifecycle below also passes.
Extended lifecycle and complete upgrade evidence remain gates.

## Legacy identity mapping preparation

Migration 022 adds `alert_policy_rule_bindings`, linking policy/node pairs to
stable rule IDs and recording legacy versus materialized origin. Schema migration
only creates the table. `MigrateLegacyAlertPolicies` is an internal transactional
cutover preparation method, not an administrator endpoint. Production evaluation
uses the combined `PrepareAlertPolicyRules` transaction on every tick, including
startup and subsequent newly created legacy rules. Once bound, use the policy UI
to edit/delete the rule; legacy endpoints return an actionable 409.

Each unmapped old rule becomes an explicit-node policy with its original rule ID,
channel, enabled flag, cooldown, configuration and timestamps. Its initial key is
`legacy:<rule ID>` so previously independent rules remain independent. An original
explicit zero recovery window stays zero. Existing rules, incident fingerprints,
active state, notification IDs and rendered outbox payloads remain untouched.
Repeated preparation skips already-bound rules rather than overwriting edits.

The entire batch holds the SQLite writer reservation. Identity conflicts or a
combined policy count above 1000 fail without partial inserts. This is a cutover
precondition, not a schema-upgrade blocker. A failed preparation aborts that tick;
startup workers do not begin until an entire evaluation succeeds. Already-running
workers keep checking current policy applicability before sending existing jobs.
The API reports preparation failure and the prior successful application time.
Databases beyond the
current policy limit need a reviewed capacity extension before cutover, not silent
truncation or rule deletion.

Regression evidence covers active incident and pending-delivery identity, unchanged
snapshots/payloads, idempotence, no duplicate deliveries, identity collisions and
capacity rollback. Full release upgrade validation and configuration transfer
remain incomplete.

## Execution-rule synchronization

Old rule update/delete endpoints reject bound rules with 409 and an actionable
policy-management message. Binding absence is checked in the modifying SQL itself,
so a separate precheck cannot race with migration. Rejection classification never
retries the write. Unbound legacy rules retain their existing editing behavior.
Current v1 configuration transfer contains nodes, targets, groups and site settings;
it does not write alert rules, policies, channels or templates. Imported node tags
must participate in the next policy selection. Portable policy transfer remains a
separate versioned contract with destination references and credential handling.

`PrepareAlertPolicyRules` combines legacy mapping and execution-rule synchronization
under one writer transaction. Runtime cutover should use this combined operation:
if synchronization fails, no new policy or binding survives and previously unbound
legacy rules remain editable. A retry maps the latest committed legacy values.
Tests inject a materialization failure after mapping, verify complete rollback and
successful retry, and verify that successful preparation preserves active incident
and pending-delivery identities. Production ticks now call this preparation API
before incident reconciliation and observation. Concurrent ticks on the same
service are serialized; notification network requests never hold that lock.

Observation writes and delivery validation also check the bound policy's current
selection and semantic rule snapshot in their existing transaction. This closes
the interval between saving a definition/changing node tags and materializing the
next execution-rule set: disabled, overridden, out-of-scope or changed rules cannot
produce a new observation or pass a delivery claim/pre-send lease check. Cosmetic
name changes remain valid. Unbound legacy rules retain their prior behavior.
The checks use current database tags, not the evaluator's older node object.
Tests cover pending and already-leased jobs for each of these changes without
calling synchronization, including a cosmetic-edit control case.

Network I/O remains outside database transactions. A configuration change after
the last lease check cannot recall a request already sent; preserve ambiguous
attempt outcomes rather than promising exact-once cancellation. Policy validation
adds bounded resolver work per managed observation/delivery; its capacity cost
still needs measurement before any supported-scale claim.

`SyncAlertPolicyRules` resolves policies against a consistent node/tag snapshot
inside a writer transaction. Selected policy/node pairs receive stable bound rule
IDs. Existing legacy bindings retain original IDs, timestamps and snapshots when
their effective configuration is unchanged. New bindings materialize ordinary
execution rules for the existing incident evaluator and outbox.

No-longer-selected rules are disabled, so the next incident reconciliation closes
their faults with a management reason (`rule_disabled`), not a technical recovery.
When a fallback policy becomes selected again, it reuses its prior rule ID. Policy
deletion removes bound execution rules transactionally only when its revision
matches; incident history remains in the independent incident tables. Pending
delivery reconciliation remains the existing incident worker's responsibility.

Synchronization limits are 10000 nodes and 100000 retained bindings. Exceeding a
limit rolls back; these are work bounds, not tested capacity claims. Validate the
legacy rule count before deployment: an unsupported dataset can prevent initial
evaluation and therefore notification worker startup. Do not silently discard
rules to fit the limit. Service tests cover dynamic tags, newly matching nodes,
management closure and restarting the evaluator without duplicate notifications.

## Administrator API

`web/src/admin-api.ts` provides typed create/update/delete/read/list and effective
preview clients. Scope types distinguish global, explicit-node and tag selectors;
updates require revisions and every read preserves the evaluation-enabled flag.
These clients reuse session, CSRF and private-cache behavior. The notification
center's scoped-policy tab uses them for configuration and inheritance preview.

All `/api/v1/admin/alert-policies` routes require administrator sessions and return
`Cache-Control: no-store`, including authentication failures. Mutations require
CSRF. Responses include `evaluation_enabled: true` and an `evaluation` object with
`state` (`pending`, `ready`, `error`) and optional `last_applied_at`. This describes
the last preparation on this service instance, not delivery success or proof that
a just-saved revision has already applied. A restart begins in pending state;
failure preserves the previous successful timestamp. Refresh to read new status.
The production entry point passes its running alert service to
`httpapi.NewWithAlertService`; the HTTP server must not create a second dormant
evaluator for status reads. The authenticated incident-pipeline test advances the
injected runtime and verifies the API changes from pending to ready with its exact
application timestamp before checking actual webhook retry/recovery behavior.

- `GET /`: list at most 100 definitions (default 50), ordered by ID; `after` and
  `next_cursor` provide keyset pagination. Concurrent changes can alter subsequent
  pages; pagination is not a cross-request snapshot.
- `GET /:policyID`: read configuration and current revision.
- `GET /effective/:nodeID`: preview matching policies, selected IDs and reasons.
- `POST /`: create; revision must be omitted/zero and enabled must be explicit.
- `PUT /:policyID`: replace using a positive current revision.
- `DELETE /:policyID?revision=N`: delete only the specified current version.

Writes are limited to 64 KiB and reject unknown fields and trailing JSON. The
alert service validates configuration semantics. Invalid input returns 400;
revision/scope conflicts return 409; absent reads return 404. Database errors are
generic 500 responses. Operations have five-second query deadlines. Audits contain
only revision, enabled state and scope kind, not selector values or channel secrets.

Contract tests cover authentication/cache headers, CSRF, creation, scope conflicts,
unknown/malformed/oversized inputs, stale edits/deletes, effective previews and
explicit pending runtime status. Full upgrade and real-browser integration remain
release requirements even when these endpoints pass.

## Management UI

Administrator rule reads include optional `policy_id` from the binding table in
the same query. Managed rule cards identify policy ownership and open the owning
policy directly for editing, including when that policy is outside the first list
page. They do not offer individual execution-rule deletion. The policy editor
fetches its current revision by ID rather than reusing stale rule configuration.
Backend ownership guards still reject stale clients racing with migration.

The notification center has a scoped-policy tab with paginated listing, create/edit,
revision-preserving conflict errors and explicit delete confirmation. Select global,
explicit-node or dynamic-tag scope; tags use one value per line and explicit any/all
semantics. Per-kind thresholds show base units, duration/repeat/cooldown fields are
seconds, and unsafe integer byte values are rejected instead of silently rounded.
Recovery duration can follow the trigger window or be explicitly set, including
zero for immediate recovery. Editing preserves that distinction after integration
with the incident engine.
Existing template IDs are retained on edit; template selection is not yet exposed.

Node previews show each independent key, its selected policy, ordered overridden
candidates and selection reason. Detail lookups are batched at eight requests.
Changing nodes clears the prior preview; failed queries never present stale results
under the new selection. Unmount ignores pending responses. A status panel shows
pending/success/error preparation and its last successful time. It explains that
saves apply on the next evaluation (normally every 15 seconds), and previews and
successful preparation do not prove notification delivery.

`scripts/alert-policies-ui.cjs` verifies synthetic-API create, tag round-trip,
conflict-preserved edits, preview and cancel/confirm delete at 360/768/1440 px in
light/dark themes. It saves temporary screenshots and checks page errors and
horizontal overflow. This is presentation coverage, not live notification delivery.
`TestLivePolicyBrowserLifecycle` and `scripts/alert-policies-live-ui.cjs` exercise
the embedded UI with actual authenticated APIs, a temporary on-disk database, the
shared production alert service and a local HTTP webhook receiver. The browser
creates a tag-scoped expiry policy, waits for its real firing incident and delivered
job, reloads and opens the policy through its execution rule, then disables it.
The test checks management closure without a recovery notification or duplicate
delivery, and captures light/dark 360/768/1440 layouts. API responses are not mocked;
the evaluator uses its normal 15-second interval. Login uses the real login API.

Opt in with `MYPROBE_TEST_POLICY_BROWSER=1` and run
`go test ./internal/httpapi -run '^TestLivePolicyBrowserLifecycle$' -count=1 -v`.
Node, Playwright (optionally located with `PLAYWRIGHT_MODULE`) and Edge are required.
Fixtures, random credentials and local receiver exist only for the test. Ordinary
CI skips it unless those prerequisites and the opt-in flag are configured.
This proves the basic live lifecycle, not cross-release upgrades, real external
notification providers or every policy conflict/recovery scenario. Complete release
upgrade validation, portable configuration transfer and capacity evidence remain.
