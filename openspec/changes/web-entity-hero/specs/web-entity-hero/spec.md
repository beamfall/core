## ADDED Requirements

### Requirement: Parent entity hero

The web viewer's entity page SHALL open with a detail stage drawn only from data the Entity
carries: its `backdrop` artwork slot behind a scrim, its poster, its title, a facts line (kind
label, year, rating, community score, up to three category Entities), the next-up child label and
Play action, an unwatched-count badge on the poster, and its `summary` field as the overview. Each
part SHALL be omitted when its data is absent.

#### Scenario: Entity with backdrop and summary

- **WHEN** a viewer opens an Entity whose artwork slots include a backdrop and whose fields include
  a non-empty `summary`
- **THEN** the page shows the backdrop behind the header and the summary as the overview, and the
  summary is not repeated in the field list

#### Scenario: Entity without artwork or overview

- **WHEN** a viewer opens an Entity with no backdrop, no poster and no `summary` field
- **THEN** the header shows the title and facts with no empty image frames or overview block

#### Scenario: Unwatched count

- **WHEN** an Entity's child containers report remaining counts of 3 and 2
- **THEN** the poster shows an unwatched badge of 5

### Requirement: Parent entity rows

The entity page SHALL render related Entities after the header in this order: container groups as
a poster row whose cards state the child's item count when known, the Entity's own Media, person
groups as the headshot cast row, then any remaining groups.

#### Scenario: Child containers with counts

- **WHEN** an Entity relates to child containers that report a total count of 8
- **THEN** each child card states "8 items"

#### Scenario: People

- **WHEN** an Entity relates to person-archetype Entities
- **THEN** they render as headshot cards linking to each person's page
