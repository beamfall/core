# entity-related-entities Specification

## Purpose
How an Entity read shows the Entities it is related to: relations are listed in both directions, filtered by the same visibility funnel as every other read, ordered by relation order and then title, and shown on the Entity view ahead of its Media.

## Requirements

### Requirement: An Entity read lists its related Entities in both directions

The `entity(id)` read SHALL return the Entities related to the Entity in both directions, grouped
by kind and direction: `child` for Entities that point at it, `parent` for Entities it points at.
Each group SHALL carry the kind, its direction, the owning plugin's singular and plural display
names when declared, and its Entities. An Entity linked through several rows SHALL appear once, in
the parent group when linked both ways, and the Entity itself SHALL NOT appear.

#### Scenario: An album names an artist

- **WHEN** an album Entity points at an artist Entity through an album-artist relation
- **THEN** the artist's read lists the album in a child album group, and the album's read lists
  the artist in a parent artist group

#### Scenario: A middle container in a three-level graph

- **WHEN** a child container points at a middle container and the middle container points at a
  parent container whose kind sorts first
- **THEN** the middle container's read lists the parent in a parent group and the child in a child
  group

#### Scenario: An Entity with no Entity relations

- **WHEN** an Entity has only Entity-to-Media relations
- **THEN** its read returns no related groups

#### Scenario: A kind with a declared plural

- **WHEN** the plugin declares the album kind with display name "Album" and plural display name
  "Albums"
- **THEN** the album group carries both, and a kind declared without a plural carries no plural

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

Groups SHALL be ordered by kind name and then direction, child groups before parent groups. Within
a group, Entities whose relation row carries a sort order SHALL come first in that order, followed
by the rest by title and then id.

#### Scenario: Mixed ordered and unordered relations

- **WHEN** two related Entities carry sort orders 2 and 1 and a third carries none
- **THEN** the group lists sort order 1, then 2, then the unordered Entity

#### Scenario: One kind related both ways

- **WHEN** an Entity points at one Entity of a kind and another Entity of the same kind points at it
- **THEN** the read lists a child group for that kind followed by a parent group for it

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
