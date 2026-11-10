## ADDED Requirements

### Requirement: Proposal before implementation

Work that adds or changes behaviour SHALL have an OpenSpec change committed under
`openspec/changes/<id>/` before any implementation commit for that work.

#### Scenario: New behaviour

- **WHEN** an agent starts work that adds or changes behaviour
- **THEN** the branch's first commit contains `proposal.md` and the spec delta for that change

#### Scenario: Bug fix against an existing requirement

- **WHEN** a fix restores behaviour that an existing requirement or ADR already states
- **THEN** the commit cites that requirement or ADR id and no new change is required

### Requirement: Ticket linkage

Each change SHALL name its Corvint Tasks ticket ID in `proposal.md`, and `tasks.md` SHALL NOT be
used as a work queue.

#### Scenario: Proposal names its ticket

- **WHEN** a change is proposed
- **THEN** `proposal.md` contains the ticket ID from `queue:core:main`

#### Scenario: Follow-up work is found

- **WHEN** work outside the change's scope is found while implementing it
- **THEN** it is filed as a new ticket and not appended to `tasks.md`

### Requirement: Existing documents stay canonical

ADRs, `docs/` pages and the contract registry SHALL remain the canonical source for behaviour that
no change has touched since adoption.

#### Scenario: Change touches a documented capability

- **WHEN** a change modifies a capability that has no spec in `openspec/specs/`
- **THEN** the change adds a spec for that capability only and links the ADR or page it derives from

### Requirement: No private material

An OpenSpec artifact in this repository SHALL NOT contain private ADR text, entitlement or
authentication mechanics, or release evidence.

#### Scenario: Proposal depends on a private decision

- **WHEN** a proposal relies on a decision recorded in a private ADR
- **THEN** it cites the ADR id and does not quote or paraphrase the ADR body

### Requirement: Validation and archive

A change SHALL pass `openspec validate --strict` before review and SHALL be archived, with its
spec delta applied to `openspec/specs/`, before its ticket is completed.

#### Scenario: Change lands

- **WHEN** the implementation of a change has merged
- **THEN** the change is moved to `openspec/changes/archive/` and its ticket is completed afterwards

#### Scenario: Validation fails

- **WHEN** `openspec validate --strict` reports an error for a change
- **THEN** the change is not submitted for review until the error is fixed
