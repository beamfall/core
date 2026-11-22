## ADDED Requirements

### Requirement: Scraper-declared container membership follows the identification

When an identification is applied through a scraper that declares a non-container EntityMap onto a
container kind's single-holder Media relation, the server SHALL link the Media to the container
Entity the lookup hydrates for that map and SHALL remove the Media's existing rows under that
relation when the lookup hydrates none. A scraper that declares no such map SHALL leave the Media's
container links as they are.

#### Scenario: Two members of one container

- **WHEN** two Media items are identified through lookups whose documents name the same container
  object
- **THEN** one container Entity exists, titled from the object's name, and holds both items

#### Scenario: The new match belongs to no container

- **WHEN** a Media item held by a container Entity is identified again through a lookup whose
  document names no container object
- **THEN** the Media item is no longer linked to that container Entity

#### Scenario: The new match belongs to another container

- **WHEN** a Media item held by a container Entity is identified again through a lookup whose
  document names a different container object
- **THEN** the Media item is held only by the other container Entity

### Requirement: Scraper-declared container members are ordered by year

A single-holder link written from such a map without an ordinal of its own SHALL take the identified
Media's year as its sort order, so the container lists its members in release-year order. A link
whose map supplies an ordinal SHALL keep that ordinal.

#### Scenario: Members listed in release order

- **WHEN** a container holds an item identified with the year 2003 and an item identified with the
  year 1999
- **THEN** the container's Media list shows the 1999 item first
