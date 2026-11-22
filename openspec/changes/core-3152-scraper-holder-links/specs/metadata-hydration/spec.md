## ADDED Requirements

### Requirement: Scraper-declared container membership follows the identification

When a scraper declares a non-container EntityMap onto a container kind's single-holder Media
relation, applying its identification SHALL link the Media to the container Entity the lookup
hydrates for that map, recording the scraper as the link's writer. When that map ran and hydrated
no Entity, the server SHALL remove the Media's link under that relation. A scraper without such a
map SHALL leave the Media's container links as they are.

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

### Requirement: Scraper container cleanup removes only the scraper's own links

The removal of a stale single-holder link SHALL delete only rows the applying scraper wrote from an
Entity of the map's kind, and SHALL keep every other row, including an operator's link. When the
engine skipped a map because its path names an unknown ordinal, the server SHALL remove nothing
under that map's kind and relation, even when a sibling map onto them hydrated nothing.

#### Scenario: An operator's link survives

- **WHEN** an operator linked a Media item to a container Entity and the item is identified through
  a lookup whose document names no container object
- **THEN** the Media item is still held by that container Entity

#### Scenario: A skipped map keeps the link

- **WHEN** a Media item held by a container Entity is identified again while the map's path names an
  ordinal the parser does not know
- **THEN** the Media item is still held by that container Entity

#### Scenario: A skipped map protects the link from an empty sibling map

- **WHEN** a Media item held by a container Entity is identified again while one map's path names an
  ordinal the parser does not know and a second map onto the same relation hydrates nothing
- **THEN** the Media item is still held by that container Entity

### Requirement: A scraper titles only the container it linked

Linking a Media item to an existing container Entity SHALL keep that Entity's stored title; a new
container Entity whose document gives no name SHALL take the provider id as its title. A container
EntityMap SHALL apply its title, fields and artwork only to a container Entity the same lookup
linked through the scraper's single-holder map, when the scraper declares one for that kind.

#### Scenario: A container object without a name keeps the title

- **WHEN** a Media item held by a titled container Entity is identified again through a lookup whose
  container object has an id but no name
- **THEN** the container Entity keeps its title

#### Scenario: A container object without an id leaves an operator's container alone

- **WHEN** an operator linked a Media item to a container Entity and the item is identified through
  a lookup whose container object has a name but no id
- **THEN** the container Entity keeps its title

### Requirement: Scraper-declared container members are ordered by year

A single-holder link written from such a map without an ordinal of its own SHALL take the Media's
stored year, after field precedence, as its sort order, so the container lists its members in
release-year order and a manual year outranks the scraper's. A link whose map supplies an ordinal
SHALL keep that ordinal.

#### Scenario: Members listed in release order

- **WHEN** a container holds an item identified with the year 2003 and an item identified with the
  year 1999
- **THEN** the container's Media list shows the 1999 item first

#### Scenario: A manual year orders the member

- **WHEN** a container member has a manual year of 2010 and its lookup reports 1999, and another
  member is identified with the year 2003
- **THEN** the container's Media list shows the 2003 item first
