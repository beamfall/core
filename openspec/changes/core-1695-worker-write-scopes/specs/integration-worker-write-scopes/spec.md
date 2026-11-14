## ADDED Requirements

### Requirement: Scoped Worker writes require an approved, audited grant

The Integration Host SHALL serve the `write-metadata`, `write-collections`, `write-artwork`,
`write-watch-state` and `read-playback-state` methods only to a Worker whose verified manifest
declares the kind and whose grant for that kind is owner-approved, SHALL write one plugin-attributed
audit record before each call takes effect, and SHALL deny the call when no audit ledger is wired or
the audit write fails.

#### Scenario: Unapproved grant

- **WHEN** a Worker calls a scoped write method whose grant is declared but not approved
- **THEN** the call fails with permission denied and nothing is written or audited

#### Scenario: Audit ledger unavailable

- **WHEN** the audit write fails or no audit ledger is wired
- **THEN** the call fails and the write does not happen

### Requirement: Scoped Worker writes stay inside the grant's Library scope

The Integration Host SHALL resolve the grant's Library binding into a Library filter and SHALL
refuse any call whose target media, collection or Library is not visible through that filter.

#### Scenario: Target outside the scope

- **WHEN** a Worker targets media in a Library its grant does not reveal
- **THEN** the call fails as not found and nothing is written or audited

#### Scenario: Collection and artwork through the scope

- **WHEN** a Worker with approved `write-collections` and `write-artwork` grants creates a collection
  in a Library its grant reveals, adds visible media and sets artwork on that media
- **THEN** the collection and artwork are stored and three audit records name the plugin

### Requirement: Watch state and playback reads are bound to one Profile

The Integration Host SHALL serve `write-watch-state` and `read-playback-state` only for a grant bound
to one Profile, acting on that Profile alone.

#### Scenario: All-Libraries grant

- **WHEN** a `write-watch-state` or `read-playback-state` grant is the all-Libraries binding
- **THEN** the call fails with permission denied

### Requirement: Scoped Worker writes spend one causation chain with their descendants

Every scoped write SHALL be admitted by the shared causation cap with a Core-issued token before
its seam runs (ADR-0125). Admission SHALL bind each outbox row the seam will append to that token,
and event delivery SHALL hand such a row back with the same token rather than a fresh chain root.
The binding ledger SHALL be bounded, and a full ledger SHALL refuse the write without spending any
budget.

#### Scenario: Worker reacts to its own write

- **WHEN** a Worker writes metadata or watch state and then receives the outbox row that write
  appended
- **THEN** the row carries the token the write spent, and a Worker that writes again on every such
  row is refused once the chain's global budget is spent

#### Scenario: Ledger full

- **WHEN** binding a write's rows would exceed the ledger bound
- **THEN** the write is refused with a tracking-capacity dead letter and its seam is not called
