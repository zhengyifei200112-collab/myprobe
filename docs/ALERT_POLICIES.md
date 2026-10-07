# Scoped alert policies (ALT-02)

Status: implementation in progress. The scope resolver and transactional storage
exist; administrative APIs/UI, evaluator integration and legacy migration are pending. No running alert
behavior changes yet. This branch starts from main; incident/outbox work in PR #50
is a dependency for state-preserving integration, not implicitly merged here.

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
policy references it; the future administrative API must explain that conflict.

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
