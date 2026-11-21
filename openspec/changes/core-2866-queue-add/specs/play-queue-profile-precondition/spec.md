## ADDED Requirements

### Requirement: Queue adds enforce current eligibility and report their committed placement

The server SHALL check current Reveal and certification eligibility for the target of
`queueAdd` atomically with the write, including when `expectedProfileID` is omitted.
A refusal SHALL leave the Play Queue unchanged. A target outside the caller's visibility
SHALL return `NOT_FOUND`. A visible target blocked by a certification ceiling SHALL
return `FORBIDDEN`. After committing an add the server SHALL return the committed
visible order even if optional card details cannot be read.

#### Scenario: Eligibility changes after the client checks it

- **WHEN** the client checks an eligible target and a relock or certification change blocks it before `queueAdd` applies
- **THEN** the server returns the applicable typed refusal
- **AND** the Play Queue is unchanged

#### Scenario: Card details fail after the queue transaction commits

- **WHEN** `queueAdd` commits and the response's optional card reads fail
- **THEN** the response succeeds with the committed visible queue order
