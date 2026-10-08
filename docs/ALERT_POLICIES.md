# Scoped alert policies (ALT-02)

Status: implementation in progress. The scope resolver and transactional storage
and administrative APIs/UI exist; evaluator integration and legacy migration are pending. No running alert
behavior changes from scoped policies yet. This branch started from main and now
includes incident/outbox dependency commit `99d08aa` from PR #50 for integration.
The GitHub PR remains unmerged; review this policy branch against that dependency.
Scoped-policy runtime activation is still pending.
The mapping preparation described below is implemented; coordinated runtime
cutover remains pending and is not called automatically at startup.

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
It currently returns resolver decisions internally; this does not mean that the
runtime evaluator applies them. Configuration transfer and legacy migration still
require integration. Database backups include the new tables, but production
restore/upgrade acceptance must be performed after all feature migrations converge.

Tests exercise cross-connection conflicting saves, stale edits/deletes, disabled
draft activation, invalid node references, channel deletion restrictions, dynamic
tag preview and reopen persistence. Store and resolver tests are required for this
foundation; endpoint authorization and browser tests become gates when those
surfaces are added.

## Legacy identity mapping preparation

Migration 022 adds `alert_policy_rule_bindings`, linking policy/node pairs to
stable rule IDs and recording legacy versus materialized origin. Schema migration
only creates the table. `MigrateLegacyAlertPolicies` is an internal transactional
cutover preparation method, not an administrator endpoint or an automatic startup
action. It must be integrated with write/evaluation coordination before activation.

Each unmapped old rule becomes an explicit-node policy with its original rule ID,
channel, enabled flag, cooldown, configuration and timestamps. Its initial key is
`legacy:<rule ID>` so previously independent rules remain independent. An original
explicit zero recovery window stays zero. Existing rules, incident fingerprints,
active state, notification IDs and rendered outbox payloads remain untouched.
Repeated preparation skips already-bound rules rather than overwriting edits.

The entire batch holds the SQLite writer reservation. Identity conflicts or a
combined policy count above 1000 fail without partial inserts. This is a cutover
precondition, not a schema-upgrade blocker: legacy evaluation remains available.
Do not activate policy evaluation when preparation fails. Databases beyond the
current policy limit need a reviewed capacity extension before cutover, not silent
truncation or rule deletion.

Regression evidence covers active incident and pending-delivery identity, unchanged
snapshots/payloads, idempotence, no duplicate deliveries, identity collisions and
capacity rollback. Runtime rule materialization, management-change handling,
legacy write compatibility and configuration transfer remain incomplete.

## Administrator API

`web/src/admin-api.ts` provides typed create/update/delete/read/list and effective
preview clients. Scope types distinguish global, explicit-node and tag selectors;
updates require revisions and every read preserves the evaluation-enabled flag.
These clients reuse session, CSRF and private-cache behavior. The notification
center's scoped-policy tab uses them for configuration and inheritance preview.

All `/api/v1/admin/alert-policies` routes require administrator sessions and return
`Cache-Control: no-store`, including authentication failures. Mutations require
CSRF. Responses include `evaluation_enabled: false`: saved policies are not yet
the runtime evaluation authority.

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
explicit non-execution status. Runtime integration and legacy migration remain
release requirements even when these endpoints pass.

## Management UI

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
under the new selection. Unmount ignores pending responses. A prominent banner
explains that saved scoped policies do not yet drive the runtime evaluator.

`scripts/alert-policies-ui.cjs` verifies synthetic-API create, tag round-trip,
conflict-preserved edits, preview and cancel/confirm delete at 360/768/1440 px in
light/dark themes. It saves temporary screenshots and checks page errors and
horizontal overflow. This is presentation coverage, not live notification delivery.
Real API/browser integration and runtime migration remain open.
