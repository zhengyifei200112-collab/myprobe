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

`TestEncryptedPolicyRestoreDeliversWithOriginalChannelKey` additionally encrypts
the consistent database snapshot with the production archive format, decrypts it,
stages/applies it into a new database destination and starts evaluation again.
With the original channel encryption key, the original queued notification reaches
a local HTTP receiver with the expected authorization header and is marked delivered.
A different channel key produces `channel_configuration` without sending; a wrong
archive passphrase is rejected. The test also checks that the database does not
contain the fixture channel credential in plaintext. Archive passwords and channel
encryption keys are separate requirements: possessing the backup password does not
replace the original channel key. This does not validate cross-release upgrade or
external provider availability.

The legacy migration tests now continue through policy cutover as well as incident
migration. A fixture built from migrations before 015 is upgraded, closed/reopened,
and prepared twice (one mapped rule, then zero). It preserves the incident and job
IDs, payload, millisecond retry deadline, attempt numbering and explicit immediate
recovery. The state matrix also prepares all legacy rules and reconciles incidents
before checking sent/failed active faults, pending triggers, sent/failed recoveries
and disabled active rules. No extra migration notification is created. This is
schema-fixture coverage; it does not replace an archived released binary/database
upgrade or convergence of other feature branches' migration numbers.

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

CI run 37912080808 timed out in the alert startup test while creating a rule in a
shared in-memory database. A separate regression confirmed that independent
`:memory:` Stores previously shared the fixed SQLite database name and inherited
each other's nodes. Each Open now uses a unique shared-cache name, preserving
intra-Store pooling but isolating separate instances. The regression and 20 local
repetitions of the worker/startup tests pass; Linux CI must verify the timeout
does not recur. Do not treat local repetition as proof of its complete cause.
Linux CI run 38046872412 subsequently passed for `eb3a4c2`; this confirms that run
completed, not that every possible SQLite cancellation/locking race is excluded.

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
The tag-selection regression now uses real configuration export/import: dry-run
does not change the selected policy, committed tags change the preview immediately,
and the next synchronization changes execution rules. Import preserves policy
versions and does not directly rewrite the existing execution-rule timestamp.

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

Synchronization constructs one validated `alertpolicy.Resolver` per transaction
and reuses it across nodes. The resolver owns copies of policy and selector slices;
caller edits and returned decision edits cannot mutate its validated state. There
is no cross-transaction cache: each new synchronization sees current definitions.
Individual observation/delivery checks still validate their own current snapshot.

Microbenchmark on Windows amd64, Intel i5-13490F, 100 matching policies × 100 nodes:
`go test ./internal/alertpolicy -run '^$' -bench '^BenchmarkPolicyNodeSelection$' -benchmem -count=1`
measured 6.95 ms / 8,292,543 B / 52,507 allocations per cycle with validation for
every node, versus 2.78 ms / 3,610,602 B / 31,616 allocations with a snapshot per
cycle (including snapshot construction). This single local sample measures only
selection, not SQLite I/O, evaluation, notifications or supported deployment size.

The on-disk warm-preparation benchmark includes migration checks, policy/node reads,
selection and rule synchronization in SQLite WAL transactions. Reproduce with:
`go test ./internal/store -run '^$' -bench '^BenchmarkPrepareAlertPolicyRules$' -benchmem -benchtime=3x -count=1`.
On Windows amd64 / Intel i5-13490F, ten matching tag policies per node gave:

| Nodes | Retained bindings | Mean preparation | Allocated bytes/cycle |
| --- | --- | --- | --- |
| 5 | 50 | 1.90 ms | 155,053 |
| 50 | 500 | 15.07 ms | 915,890 |
| 100 | 1,000 | 32.40 ms | 1,761,090 |
| 500 | 5,000 | 155.12 ms | 8,100,525 |

These are three timed iterations per fixture, with creation and initial
materialization excluded. Final row counts verify no rule/binding growth. The
database is a temporary local file with the Store's normal WAL/NORMAL settings;
results include warm OS caches and are not durable-storage throughput guarantees.
The 500-node case is exploratory. None of these averages establishes query P95,
first-start latency, concurrent administration cost, complete evaluator throughput,
notification throughput or 24-hour stability. Those remain separate acceptance work.

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

### Portable policy bundle (export implemented; import pending)

`GET /api/v1/admin/alert-policies/export` downloads authenticated, no-store JSON
with `format: "myprobe-alert-policies"`, `version: 1`, and a `policies` array. This
is separate from the node configuration snapshot version. Each item includes
`source_id`, name, policy key, enabled flag, priority, scope, source channel ID,
kind, typed rule configuration and cooldown. Source node IDs and template ID are
references, not automatically valid destination objects. Import must explicitly
map those references, validate the complete batch and commit atomically; it is
not yet implemented. No destination overwrite semantics are implied by source IDs.

Export excludes channel names/credentials, policy revisions/timestamps, execution
bindings, events and queued jobs. Configuration uses a field whitelist; unsupported
legacy fields reject the whole export rather than silently dropping semantics.
Persisted omitted recovery duration and explicit zero remain distinct. Empty
exports contain an empty array. Tests cover authenticated download/cache behavior,
reference preservation, recovery representation and excluded fields. The page
download control and import preview/apply workflow remain outstanding.

The Store now provides `CreateAlertPolicies` for atomic create-only batches of
1–1000 definitions (also subject to the total 1000-policy limit). It acquires one
writer reservation and reuses single-policy structural/reference/conflict checks.
A later failure rolls back earlier inserts. Dry-run executes the same validation
and explicitly rolls back; returned preview IDs are temporary and must not be
treated as destination IDs. Existing IDs/revisions are rejected. Semantic threshold
and template validation still belong at the service boundary before invoking it.
This method is not an import endpoint. Reference mapping, request idempotency,
preview/apply contracts and UI remain unfinished; in particular disabled duplicate
batches require request-level deduplication before public exposure.

`alerts.Service.PreviewPolicyImport` now maps every source channel, explicit node
and nonempty template reference through supplied destination-ID maps, including
same-environment imports (no implicit ID reuse). It rejects unsupported bundle
versions, duplicate/empty source IDs, missing mappings, invalid thresholds and
unavailable destination references. Single-policy semantic validation is reused;
the complete destination batch runs through Store dry-run validation for scope
conflicts and capacity. Returned definitions keep their source IDs for review,
with normalized destination references, and expose no temporary generated IDs.
Inputs are not mutated. This is an internal service API; HTTP preview/apply,
idempotent commit and management-page controls remain pending.

Migration 023 adds a durable policy-import request ledger. The internal Store
`ApplyPolicyImport` operation reserves the same writer transaction for request
lookup, batch creation and result recording. A matching request ID/digest replays
the original ordered policy IDs; different content under the same ID conflicts.
The digest must cover the complete canonical bundle, mappings and options at the
service boundary. Failed batches leave neither definitions nor a request record.
Retries do not recreate policies deleted after the original import. Tests cover
competing database connections, rollback, digest conflicts and reopen/deletion.
The ledger retains at most 10000 requests and then rejects new imports; records
are not silently expired, since expiration could allow an old request to create
duplicates. This is an internal commit primitive, not a public import endpoint.
Service-level digest/replay handling, HTTP preview/apply and UI are still required.
Reconcile migration numbering with other unmerged feature branches before release.

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

### Import retry identity

The service hashes the typed bundle and destination mappings before normalization.
An existing request key with the same digest returns the original ordered policy
IDs before validating mutable destination references. Deleting a created policy,
node, template or channel does not make a retry recreate objects. These IDs describe
the historical import result, not a guarantee that the objects still exist.
Changing the bundle or mappings under that key returns a conflict. JSON map key
order does not affect the digest; array order and null versus empty maps do.
The store repeats the identity check under its writer transaction so concurrent
callers cannot commit duplicate batches. Failed new imports leave no replay record.

`TestPolicyImportApplyReplaysAfterReferencesDisappear` covers service recreation,
deleted references, changed content/mappings and invalid request keys. Store tests
cover two connections and database reopen. This is service/store coverage; HTTP
apply route and the import UI are still pending.

`POST /api/v1/admin/alert-policies/import/preview` accepts `{bundle, mapping}`
under administrator session and CSRF protection. It strictly decodes the portable
types, rejects trailing JSON and unknown fields, and limits the body to 16 MiB
(single-policy editing retains its separate 64 KiB limit). A successful response
contains the normalized destination `bundle` and `create_count`; all responses are
private/no-store. Preview rolls back its transaction and creates no policies or
import replay records. It does not reserve references or authorize a later commit.
The apply contract must bind the confirmed content and revalidate destination state.
Policy writes recheck referenced templates inside the same writer transaction as
channel/node checks, including batch dry runs and imports. The regression test
`TestPolicyImportRejectsTemplateDeletedAfterPreparation` deletes a template after
service preparation and verifies neither policies nor replay records are committed.
This check protects commit-time validity; it does not prevent subsequent template
deletion or freeze template contents for the lifetime of a policy.
`TestPolicyImportPreviewAPI` exercises real authentication, CSRF, reference mapping,
explicit zero recovery, invalid versions/fields/references, body limits and no writes.
