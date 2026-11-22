## ADDED Requirements

### Requirement: Entity-kind browse cards are annotated without reading Media they do not count

The `libraryEntities` read SHALL annotate a page of Entity cards (class and watched counts)
without reading the Media of Entities a card does not count. A declared container's watched counts
SHALL include only the Media of the parts linked to it in its Library; any other kind's SHALL
include only the Media reached through its ordered outgoing links. The class of a card SHALL be
the same as when every linked Entity's Media were read per card.

#### Scenario: A container linked to a large shared Entity

- **WHEN** a declared container has one part holding two Media and an ordered link to a shared
  Entity holding three other Media
- **THEN** the container's card reports two Media in its watched counts

#### Scenario: An undeclared kind with an ordered link

- **WHEN** an Entity whose kind is not a declared container has an ordered link to an Entity
  holding three Media and a part linked to it
- **THEN** its card reports the three Media of the ordered link and none of the part's

#### Scenario: Results unchanged on a real catalog

- **WHEN** every page of a Library's container kind is annotated before and after this change
- **THEN** each card reports the same class and the same watched counts

### Requirement: Web reopens a Library's Entity-kind view from cached reads

The web app SHALL keep recent `libraryEntities` reads and, when a Library page opens a read it
holds, SHALL paint it at once and read it afresh, replacing the cached cards when the fresh read
arrives. Callers asking for the same read at once SHALL share one request. A read made before an
engagement change SHALL NOT be served from the cache.

#### Scenario: Library tab opened again

- **WHEN** a viewer leaves a Library's Entity-kind view and opens it again
- **THEN** the cached cards show at once, one fresh read is made for the first page, and its
  cards replace the cached ones

#### Scenario: Engagement changed in between

- **WHEN** the viewer marks an item watched (or hides one, or flips Hide watched) after a read
- **THEN** that read is not painted from the cache and the view waits for a fresh read

#### Scenario: Fresh read fails

- **WHEN** the fresh read for a cached page fails
- **THEN** the view shows its retry state as it does for any failed read
