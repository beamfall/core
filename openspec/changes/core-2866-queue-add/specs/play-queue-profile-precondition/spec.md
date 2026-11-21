## ADDED Requirements

### Requirement: Queue adds enforce current eligibility and report their committed placement

The server SHALL check current Reveal and certification eligibility for the target of
`queueAdd` atomically with the write, including when `expectedProfileID` is omitted.
A refusal SHALL leave the Play Queue unchanged. A target outside the caller's visibility
SHALL return `NOT_FOUND`. A visible target blocked by a certification ceiling SHALL
return `FORBIDDEN`. With omitted or null `expectedProfileID`, the server SHALL
apply the write to the session's current Profile using that Profile's eligibility.
An explicit precondition that fails SHALL return `CONFLICT` with reason `PROFILE_MISMATCH`.
After committing an add the server SHALL return the first page of at most 50
visible items in committed order, even if optional card details cannot be read.
For a visible target, `extensions.queueAdd` SHALL map the mutation's response key
to its `mediaID` and zero-based `position` in the visible queue, even when the
target is off-page.

#### Scenario: Eligibility changes after the client checks it

- **WHEN** the client checks an eligible target and a relock or certification change blocks it before `queueAdd` applies
- **THEN** the server returns the applicable typed refusal
- **AND** the Play Queue is unchanged

#### Scenario: Card details fail after the queue transaction commits

- **WHEN** `queueAdd` commits and the response's optional card reads fail
- **THEN** the response succeeds with the committed visible queue page and placement

#### Scenario: The session switches Profiles after the request is authorized

- **WHEN** a Profile switch applies before the queue add obtains the write lock
- **THEN** an omitted or null precondition uses the current Profile and its eligibility
- **AND** an explicit precondition for the previous Profile is refused
