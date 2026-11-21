## MODIFIED Requirements

### Requirement: An Entity read lists its related Entities in both directions

The `entity(id)` read SHALL return the Entities related to the Entity through Entity-to-Entity
relations in both directions, the Entities it points at and the Entities that point at it,
grouped by Entity kind. Each group SHALL carry the kind, the owning plugin's display name for the
kind when declared, the owning plugin's plural display name for the kind when declared, and its
Entities. An Entity reached through several relation rows SHALL appear once, and the Entity itself
SHALL NOT appear.

#### Scenario: An album names an artist

- **WHEN** an album Entity points at an artist Entity through an album-artist relation
- **THEN** the artist's read lists the album in an album group, and the album's read lists the
  artist in an artist group

#### Scenario: An Entity with no Entity relations

- **WHEN** an Entity has only Entity-to-Media relations
- **THEN** its read returns no related groups

#### Scenario: A kind with a declared plural

- **WHEN** the plugin declares the album kind with display name "Album" and plural display name
  "Albums"
- **THEN** the album group carries both, and a kind declared without a plural carries no plural

### Requirement: The Entity view shows related Entities before its Media

The web Entity view SHALL render each related group under the kind's plural display name, else
its display name, else the kind, before the Entity's Media list or detail Layout, and SHALL render
the groups when the Entity has no direct Media.

#### Scenario: An artist with albums and tracks

- **WHEN** an artist has related albums and linked tracks
- **THEN** the page shows the album group first and the tracks after it

#### Scenario: An artist linked only through albums

- **WHEN** an artist has related albums and no linked tracks
- **THEN** the page shows the album group instead of an empty page

#### Scenario: Plural heading

- **WHEN** the album group carries plural display name "Albums"
- **THEN** the group is headed "Albums"; without a plural it is headed by the display name
