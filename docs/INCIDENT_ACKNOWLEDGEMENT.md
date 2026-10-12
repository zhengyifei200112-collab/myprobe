# Incident acknowledgement contract

Roadmap ACK-01 / ALT-03. Implementation in progress on
`feature/incident-acknowledgement`, stacked on maintenance PR #55. It reuses the
incident/outbox state machine. Acknowledgement, silence and recovery are independent.
This contract covers acknowledgement; silence/combinations/aggregation remain
separate work packages. No merge or deployment is included.

## Behavior

- Only a persisted firing incident accepts a new acknowledgement. Pending and
  resolved incidents remain readable and reject new actions.
- First acknowledgement wins per incident. It records the authenticated actor ID,
  username snapshot, server UTC millisecond timestamp and optional private note.
  Changing the note or actor afterwards does not overwrite the original record.
- Acknowledgement does not resolve the fault, pause observation, cancel/claim/send
  a notification or change trigger/recovery/repeat clocks. Data can remain stale
  while someone acknowledges the fault; acknowledgement is not new technical evidence.
- A successful request ID replays the original result even after recovery or
  account rename. The digest binds incident, actor and normalized note. Reusing
  that ID with different content/actor is a conflict. A different ID for an already
  acknowledged incident is also rejected, so double clicks cannot add actions.
- Notes normalize CRLF to LF and trim surrounding Unicode whitespace. They may
  contain LF/tab, but no other remaining Unicode controls or invalid UTF-8; the
  normalized maximum is 1000 UTF-8 bytes. Empty notes are allowed.

## Persistence and privacy

Forward migration 028 adds one `incident_acknowledgements` row per incident, with
a unique original request ID and private digest. The writer is reserved before
state/replay checks. Record, retry identity and audit commit together; audit failure
rolls everything back. Audit records the presence of a note, not its text.
Actor name is frozen without an account foreign key; account changes do not erase
historical authorship. The incident foreign key cascades if incident history is
explicitly deleted. There is currently no automatic incident/ack retention purge.

Backups preserve acknowledgements and their retry identities. A pre-028 backup
migrates to an empty table, without inferred historical acknowledgements. Restoring
an earlier snapshot rewinds actions performed after it, just as it rewinds incidents
and audit; clients must review the snapshot before retrying uncertain operations.
Replay guarantees last while the original incident record is retained.

Public node/Agent payloads and notifications receive no actor/note/ack fields.
The private incident list returns only optional `acknowledgement` summary fields
`actor_name` and `acknowledged_at`, in the same read snapshot as incident state.
It does not select note text, actor IDs or request identities. Complete notes are
available only through the protected acknowledgement endpoint.

## Management API

`GET /api/v1/admin/incidents/:incidentID/acknowledgement` returns
`incident_state` and nullable `acknowledgement`. A record contains `incident_id`,
`actor_id`, `actor_name`, `acknowledged_at`, `note`; request ID/digest remain private.

`POST` to the same URL accepts only:

```json
{"request_id":"unique-operation-id","note":"Optional management note"}
```

Session/CSRF protection applies to mutations. All acknowledgement/list responses,
including auth/error responses, are private/no-store. Each acknowledgement request
has a five-second deadline. POST accepts at most 16 KiB of strict UTF-8 JSON, rejects
unknown fields and trailing values. Incident/request IDs use portable characters
and at most 128 bytes; actor and timestamp cannot be supplied by the client.
First creation returns 201; identical committed retry returns 200 and `replayed:true`.

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid_acknowledgement` | Invalid ID/note/body |
| 404 | `incident_not_found` | Missing incident |
| 409 | `acknowledgement_request_conflict` | ID belongs to different content/actor |
| 409 | `incident_already_acknowledged` | First record exists; refresh and inspect it |
| 409 | `incident_not_firing` | New action requires firing state |
| 500 | `acknowledgement_unavailable` | Storage result may be uncertain |

Authorization/CSRF errors use existing 401/403 contracts. A reused committed ID is
checked before current incident state; no promise is made for a request rejected
before commit. No plaintext note, SQL or database path appears in generic errors.

## Delivery requirements still being implemented

The incident detail UI must provide a private note form, display the original
author/time/note, and explain that acknowledgement does not silence or recover.
Persist the original uncertain request before sending, support identical retry
across reload, prevent a new operation while its result is unknown and expose
corrupt/unavailable browser storage without accidentally posting a new request.
Refresh private list summaries after successful creation or conflicts.

Store tests cover replay, different-actor concurrency, validation, no state/job
mutation, atomic audit, reopen and staged full restore. API tests cover auth/CSRF,
bounded strict input, first/replay/conflict, list-note separation and public-node
privacy. Real local receiver tests, browser lifecycle (both themes and
360/768/1440 px), released upgrade integration and final CI remain delivery gates.
