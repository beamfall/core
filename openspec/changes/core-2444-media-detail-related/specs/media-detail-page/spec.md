## ADDED Requirements

### Requirement: More with the first billed person

The web Media detail page SHALL show a row titled with the name of the item's first
person-archetype credit in the server's order, listing at most 20 other Media credited to that
person. The row SHALL leave out Media the profile muted, as other passive-discovery rows do, and
SHALL NOT list the current item, and SHALL NOT be shown when no other Media remain or the read fails. Choosing a
card SHALL open that Media item's detail page.

#### Scenario: Lead person with other titles

- **WHEN** a Media item's first person credit is credited on two other Media
- **THEN** the page shows a "More with" row named for that person with those two cards and not the
  current item

#### Scenario: Muted title left out

- **WHEN** the profile marked one of the lead person's other Media "Not interested"
- **THEN** that Media is not in the "More with" row

#### Scenario: Lead person with no other titles

- **WHEN** the first person credit is credited only on the current item
- **THEN** the page shows no "More with" row

### Requirement: Related row

The web Media detail page SHALL show the Media `relatedMedia` returns for the item as a Related
row, and SHALL NOT show the row when none are returned or the read fails.

#### Scenario: Nothing related

- **WHEN** `relatedMedia` returns no Media for the item
- **THEN** the page shows no Related row
