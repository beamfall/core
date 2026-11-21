## ADDED Requirements

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

### Requirement: Queue adds report their committed placement

After committing an add the server SHALL return the first page of at most 50
visible items in committed order, even if optional card details cannot be read.
A later failure in the same operation SHALL preserve committed queue add data
and identify the failed field by response key in the error path.

#### Scenario: Card details fail after the queue transaction commits

- **WHEN** `queueAdd` commits and the response's optional card reads fail
- **THEN** the response succeeds with the committed visible queue page in `data`

#### Scenario: The target lands outside the first page

- **WHEN** `queueAdd` commits a visible target outside the first page
- **THEN** the response stays bounded to the first page of the committed queue

#### Scenario: A later queue add alias is refused

- **WHEN** the first `queueAdd` alias commits and a later alias is refused
- **THEN** `data` includes the committed alias's queue page
- **AND** the response includes a typed error with the refused alias as its path
