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
