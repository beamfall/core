# entity-related-entities Specification

## Purpose
How an Entity read shows the Entities it is related to: relations are listed in both directions, filtered by the same visibility funnel as every other read, ordered by relation order and then title, and shown on the Entity view ahead of its Media.

## Requirements

### Requirement: An Entity read lists its related Entities in both directions

The `entity(id)` read SHALL return the Entities related to the Entity through Entity-to-Entity
relations in both directions, the Entities it points at and the Entities that point at it,
grouped by Entity kind. Each group SHALL carry the kind, the owning plugin's display name for the
kind when declared, and its Entities. An Entity reached through several relation rows SHALL
appear once, and the Entity itself SHALL NOT appear.

#### Scenario: An album names an artist

- **WHEN** an album Entity points at an artist Entity through an album-artist relation
- **THEN** the artist's read lists the album in an album group, and the album's read lists the
  artist in an artist group

#### Scenario: An Entity with no Entity relations

- **WHEN** an Entity has only Entity-to-Media relations
- **THEN** its read returns no related groups

### Requirement: Related Entities honor the visibility funnel

Related Entities SHALL be omitted when trashed or when their Library is hidden under the caller's
Library filter. The context Entity SHALL itself be visible; an Entity hidden behind the Reveal
contract SHALL fail the read the same way a missing Entity does.

#### Scenario: A related Entity in a restricted Library

- **WHEN** an Entity is related to an Entity in a restricted Library and the scope is not
  Revealed
- **THEN** the related Entity is absent from the read

#### Scenario: A trashed related Entity

- **WHEN** a related Entity is trashed
- **THEN** it is absent from the read

### Requirement: Related Entities are ordered by relation order, then title

Groups SHALL be ordered by kind name. Within a group, Entities whose relation row carries a sort
order SHALL come first in that order, followed by the rest by title and then id.

#### Scenario: Mixed ordered and unordered relations

- **WHEN** two related Entities carry sort orders 2 and 1 and a third carries none
- **THEN** the group lists sort order 1, then 2, then the unordered Entity

### Requirement: The Entity view shows related Entities before its Media

The web Entity view SHALL render each related group under the kind's display name (the kind when
none is declared) before the Entity's Media list or detail Layout, and SHALL render the groups
when the Entity has no direct Media.

#### Scenario: An artist with albums and tracks

- **WHEN** an artist has related albums and linked tracks
- **THEN** the page shows the album group first and the tracks after it

#### Scenario: An artist linked only through albums

- **WHEN** an artist has related albums and no linked tracks
- **THEN** the page shows the album group instead of an empty page
