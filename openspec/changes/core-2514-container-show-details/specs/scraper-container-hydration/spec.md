## ADDED Requirements

### Requirement: Container maps link sibling-hydrated Entities

When a container map declares `ContainerRelations`, identification SHALL link the container Entity to
each Entity a non-container map with the named relation hydrated in the same lookup, under the
container relation, keeping that Entity's sort order. A link SHALL be written only when the bundle
declares the container relation on the container kind toward that Entity's kind.

#### Scenario: Series genres and cast

- **WHEN** a series container map declares `ContainerRelations` `{"in-genre": "genre-of", "credit": "credited-in"}`
  and the lookup hydrates the Drama genre and two cast members
- **THEN** the series Entity is linked to Drama under `in-genre` and to both people under `credit` in
  billing order

### Requirement: Container links replace earlier links

A lookup that resolves a container map SHALL replace the container Entity's earlier links under every
container relation the map declares and the bundle declares on the container kind, so a relation the
lookup yields nothing for is cleared.

#### Scenario: Corrected match replaces genres and cast

- **WHEN** the series was linked to Drama and two cast members and a later identification yields Comedy and one other cast member
- **THEN** the series Entity is linked only to Comedy and the other cast member

#### Scenario: Corrected match without cast clears cast

- **WHEN** the series was linked to two cast members and a later identification's lookup has an empty cast
- **THEN** the series Entity has no `credit` links and keeps its genre links from that lookup

## MODIFIED Requirements

### Requirement: Container maps hydrate existing container Entities

A `Container` entity map SHALL hydrate the existing container Entity of its kind that holds the
identified Media, directly or up the container chain inside the Media's Library. Its fields SHALL
merge into the Entity's existing fields, and its poster and `ArtworkMap` slots SHALL be applied like
other hydrated Entity posters: a slot that already exists, is locked, or belongs to a Sealed or
Encrypted Library is not replaced. The Entity's title SHALL be kept unless the map's `TitlePath`
resolves a non-blank value, which SHALL replace the title without changing the Entity's ID. A
container map SHALL NOT create an Entity.

#### Scenario: Series and season details

- **WHEN** a Media held by a season Entity inside a series Entity is identified with container
  maps for both kinds
- **THEN** both Entities carry the mapped overview and date fields and the mapped posters

#### Scenario: No container holds the Media

- **WHEN** no Entity of the map's kind holds the Media
- **THEN** nothing is hydrated for that map and the identification still applies

#### Scenario: Matched title replaces the folder spelling

- **WHEN** a series Entity built from the folder "A Mothers Son" is hydrated by a map whose
  `TitlePath` resolves "A Mother's Son"
- **THEN** the Entity's title is "A Mother's Son" and its ID is unchanged

#### Scenario: Unresolved title keeps the folder spelling

- **WHEN** the map's `TitlePath` does not resolve
- **THEN** the Entity keeps its existing title

#### Scenario: Series backdrop

- **WHEN** a series container map declares `ArtworkMap` `{"backdrop": "backdrop_path"}`
- **THEN** the series Entity receives a backdrop
