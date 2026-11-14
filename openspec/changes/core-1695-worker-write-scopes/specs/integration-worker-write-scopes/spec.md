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
its seam runs (ADR-0125), after its targets are proven visible and before its audit row is
written. Admission SHALL bind each outbox row the seam will append to that token, and event
delivery SHALL hand such a row back with the same token rather than a fresh chain root.

#### Scenario: Worker reacts to its own write

- **WHEN** a Worker writes metadata or watch state and then receives the outbox row that write
  appended
- **THEN** the row carries the token the write spent, and a Worker that writes again on every such
  row is refused once the chain's global budget is spent

### Requirement: Descendant bindings are unique and bounded

A descendant row SHALL be named by a key no other admitted write holds: when a key is already
bound, admission SHALL spend nothing and the Host SHALL stamp the rows 1 ms later and ask again, a
bounded number of times. The binding ledger SHALL be bounded in total and per Worker, and a full
ledger or share SHALL refuse the write without spending any budget. A call refused on its targets
SHALL reserve nothing, and a call whose audit row or seam fails SHALL release what it reserved.

#### Scenario: Two chains write one item in the same millisecond

- **WHEN** a second chain's write would append a row with the same aggregate and millisecond as a
  row another chain's write already bound
- **THEN** the second write's rows are stamped 1 ms later and each row returns with its own chain's
  token

#### Scenario: Ledger full

- **WHEN** binding a write's rows would exceed the ledger bound or the Worker's share of it
- **THEN** the write is refused with a tracking-capacity dead letter and its seam is not called

#### Scenario: Refused calls hold no reservation

- **WHEN** a write is refused because a target is missing or hidden, a metadata field the store
  would reject (empty or duplicate name, zero date), its audit row fails, or its seam fails
- **THEN** no descendant binding remains for it in the ledger

### Requirement: A descendant binding outlives its row's fresh root

A binding SHALL last until a fresh root started at its row's stamp would be expired, measured from
the later of admission and that stamp, so a row stamped after admission never yields a usable root
once its chain has expired.

#### Scenario: Restamped row delivered after its chain expired

- **WHEN** a row stamped 1 ms after admission is delivered after the writing chain has expired
- **THEN** it returns with the expired chain's token, and once its binding lapses a root minted
  from its stamp is already expired
