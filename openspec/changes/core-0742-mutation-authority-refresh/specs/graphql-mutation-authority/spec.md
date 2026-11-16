## ADDED Requirements

### Requirement: Each mutation field runs under the session's current authority

When a GraphQL mutation operation selects more than one field, the server SHALL re-resolve the
calling session's authority from the store after each field runs and before the next field runs,
and SHALL authorize the next field against that refreshed authority. A later field SHALL act on
the Profile, Library grants, Reveal state and Admin Session the session holds at that point, not
on those it held when the request arrived.

#### Scenario: A Reveal PIN of the previous Profile cannot reveal the new Profile

- **WHEN** a session bound to adult Profile A sends one mutation operation with `switchProfile`
  to adult Profile B followed by `reveal` with Profile A's Reveal PIN
- **THEN** the reply is the `reveal PIN mismatch` refusal
- **AND** the session is bound to Profile B and is not revealed

#### Scenario: A later field uses the new Profile's Library and Reveal scope

- **WHEN** a revealed session bound to Profile A sends one mutation operation with `switchProfile`
  to Profile B followed by a write naming Media only Profile A's revealed scope can see
- **THEN** the write is refused as unknown Media
- **AND** neither Profile's state changes from that write

#### Scenario: A later privileged field loses the Admin Session the switch cleared

- **WHEN** a session holding an Admin Session sends one mutation operation with `switchProfile`
  followed by an Admin Session field
- **THEN** the reply is `admin session required`
- **AND** the privileged field has no side effect

#### Scenario: A session that no longer resolves stops the operation

- **WHEN** an earlier field leaves the session unable to resolve (signed out, Profile disabled or
  outside its access schedule)
- **THEN** the operation stops before the next field with an `UNAUTHENTICATED` error
- **AND** the next field has no side effect
