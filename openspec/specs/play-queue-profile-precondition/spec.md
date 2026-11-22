# play-queue-profile-precondition Specification

## Purpose
Play queue mutations can name the Profile they were made for, so the server refuses a write
that arrives after `switchProfile` rebound the session to another Profile (CORE-2177).

## Requirements

### Requirement: Play queue writes can name the Profile they were made for

`queueAdd`, `queueRemove`, `queueMove`, `queueClear` and `stationStart` SHALL accept an optional
`expectedProfileID` argument. When it is present and is not the Profile the caller's session is
bound to when the write applies, the server SHALL change no Play Queue and SHALL answer with
error code `CONFLICT` and `extensions.reason` `PROFILE_MISMATCH`. When it is absent or null the
mutation SHALL behave exactly as before.

#### Scenario: A write made for the previous Profile is refused after a switch

- **WHEN** a session bound to Profile A switches to Profile B and then sends `queueAdd` with
  `expectedProfileID` set to Profile A
- **THEN** the reply is `CONFLICT` with reason `PROFILE_MISMATCH`
- **AND** neither Profile's Play Queue changes

#### Scenario: A matching precondition applies

- **WHEN** a session bound to Profile A sends `queueAdd` with `expectedProfileID` set to Profile A
- **THEN** the item is added to Profile A's Play Queue

#### Scenario: Older clients are unaffected

- **WHEN** a client sends a queue mutation without `expectedProfileID`
- **THEN** it applies to the session's current Profile as it did before

### Requirement: A supplied precondition is validated before the write resolves

A supplied blank or whitespace-only `expectedProfileID` SHALL be refused with error code `INVALID`
and change no Play Queue. The mismatch check SHALL run before any resolution that depends on the
caller's Profile, such as a Station seed.

#### Scenario: A blank precondition is malformed, not absent

- **WHEN** a client sends any of the five mutations with `expectedProfileID` set to `""` or to
  whitespace
- **THEN** the reply is `INVALID`
- **AND** no Play Queue changes

#### Scenario: A Station seed only the previous Profile can see is a mismatch

- **WHEN** a session switches from Profile A to Profile B and then sends `stationStart` with
  `expectedProfileID` Profile A and a seed only Profile A can see
- **THEN** the reply is `CONFLICT` with reason `PROFILE_MISMATCH`, not an unknown-seed error
- **AND** no Play Queue changes

### Requirement: The Profile check is atomic with the queue write

The server SHALL NOT let a Profile rebind of the session commit between the check of
`expectedProfileID` and the commit of the queue write it guards.

#### Scenario: A concurrent switch never moves the write into the new Profile

- **WHEN** `switchProfile` from Profile A to Profile B runs concurrently with a queue mutation
  carrying `expectedProfileID` Profile A
- **THEN** the item is never found in Profile B's Play Queue

### Requirement: Queue adds enforce current eligibility

The server SHALL check current Reveal and certification eligibility atomically
with `queueAdd` and leave the Play Queue unchanged on refusal. With omitted or
null `expectedProfileID`, the server SHALL use the session's current Profile
and its eligibility.

#### Scenario: Eligibility changes after the client checks it

- **WHEN** the client checks an eligible target and a relock or certification change blocks it before `queueAdd` applies
- **THEN** the server returns `NOT_FOUND` for a hidden target or `FORBIDDEN` for a visible target blocked by certification
- **AND** the Play Queue is unchanged

#### Scenario: The session switches Profiles after the request is authorized

- **WHEN** a Profile switch applies before the queue add obtains the write lock
- **THEN** an omitted or null precondition uses the current Profile and its eligibility
- **AND** an explicit precondition for the previous Profile returns `CONFLICT` with reason `PROFILE_MISMATCH`

#### Scenario: Managed access expires during a queue add

- **WHEN** a managed Profile's access window and any session extension no longer admit the current server time at the write
- **THEN** `queueAdd` returns `UNAUTHENTICATED` and the Play Queue is unchanged

#### Scenario: An inaccessible encrypted Library cannot be read

- **WHEN** the target is in the catalog and an unsealed encrypted Library outside the current Profile's visible Libraries has a read failure
- **THEN** the server resolves the target without reading that Library

### Requirement: Queue adds report their committed placement

The server SHALL return the committed queue's first page of up to 50 items and a
receipt in `extensions.queueAdds` with the response key, target Media ID, and
visible position. The response SHALL retain both after a later batch failure.

#### Scenario: Card details fail after the queue transaction commits

- **WHEN** `queueAdd` commits and the response's optional card reads fail
- **THEN** the response succeeds with the committed visible queue page in `data`

#### Scenario: The target lands outside the first page

- **WHEN** `queueAdd` commits a visible target outside the first page
- **THEN** the response stays bounded to the first page of the committed queue
- **AND** the receipt identifies the target and its zero-based position in the full visible queue

#### Scenario: The committed target is muted

- **WHEN** `queueAdd` commits a muted target
- **THEN** the receipt omits `position`

#### Scenario: A later queue add alias is refused

- **WHEN** the first `queueAdd` alias commits and a later alias is refused
- **THEN** `data` includes the committed alias's queue page and `extensions.queueAdds` includes its receipt
- **AND** the response includes a typed error with the refused alias as its path
