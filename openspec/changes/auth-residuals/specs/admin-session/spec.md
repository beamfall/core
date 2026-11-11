## ADDED Requirements

### Requirement: Ownership transfer ends other Profiles' Admin Sessions

When the owner designation is set, the server SHALL end the Admin Session of every session that is
not bound to the newly designated owner Profile, in the same store transaction as the designation.
Sessions bound to the new owner SHALL keep their Admin Session.

#### Scenario: Ownership moves to another adult Profile

- **WHEN** the owner holds an Admin Session and ownership is transferred to another adult Profile
- **THEN** the outgoing owner's sessions carry no Admin authority on their next request, without
  waiting for the Admin Session to lapse

#### Scenario: The new owner already holds an Admin Session

- **WHEN** a session bound to the Profile being designated owner already holds an Admin Session
- **THEN** that session keeps its Admin Session after the designation
