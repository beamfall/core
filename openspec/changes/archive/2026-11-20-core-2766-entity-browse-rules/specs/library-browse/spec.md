## ADDED Requirements

### Requirement: Relation-based Entity browse eligibility

A Library plugin's compiled browse rules SHALL restrict eligibility for the declared Entity kinds by their relations in the Library. A candidate SHALL be eligible when any configured source matches. Kind totals, listed items, filtered totals and letter offsets SHALL use the same eligibility in catalog and unsealed Encrypted Libraries. Kinds without a rule SHALL keep their existing eligibility. Browse eligibility SHALL leave Entity detail, search and stored relations intact.

#### Scenario: Music Artists browse

- **WHEN** an untrashed artist is linked as an album artist from an untrashed album, or is credited on an untrashed track with no album-track relation
- **THEN** the Artists browse view includes that artist

#### Scenario: Credit on a track belonging to an album

- **WHEN** an artist is only credited on tracks with an album-track relation
- **THEN** the Artists view omits that artist and search and track credits retain the artist Entity
