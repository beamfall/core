## MODIFIED Requirements

### Requirement: An Entity read lists its related Entities in both directions

The `entity(id)` read SHALL return the Entities related to the Entity through Entity-to-Entity
relations in both directions, the Entities it points at and the Entities that point at it,
grouped by Entity kind and relation direction. Each group SHALL carry the kind, its direction
(`child` for Entities that point at the Entity, `parent` for Entities it points at), the owning
plugin's display name for the kind when declared, the owning plugin's plural display name for the
kind when declared, and its Entities. An Entity reached through several relation rows SHALL appear
once, in the parent group when it is linked both ways, and the Entity itself SHALL NOT appear.

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
