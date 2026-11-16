## ADDED Requirements

### Requirement: Play queue writes can name the Profile they were made for

`queueAdd`, `queueRemove`, `queueMove`, `queueClear` and `stationStart` SHALL accept an optional
`expectedProfileID` argument. When it is present and is not the Profile the caller's session is
bound to when the write applies, the server SHALL change no Play Queue and SHALL answer with
error code `CONFLICT` and `extensions.reason` `PROFILE_MISMATCH`. When it is absent the mutation
SHALL behave exactly as before.

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

### Requirement: The Profile check is atomic with the queue write

The server SHALL NOT let a Profile rebind of the session commit between the check of
`expectedProfileID` and the commit of the queue write it guards.

#### Scenario: A concurrent switch never moves the write into the new Profile

- **WHEN** `switchProfile` from Profile A to Profile B runs concurrently with a queue mutation
  carrying `expectedProfileID` Profile A
- **THEN** the item is never found in Profile B's Play Queue
