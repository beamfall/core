## ADDED Requirements

### Requirement: More with the first billed person

The web Media detail page SHALL show a row titled with the name of the item's first
person-archetype credit, listing the other Media credited to that person. The row SHALL NOT list
the current item, and SHALL NOT be shown when no other Media remain or the read fails. Choosing a
card SHALL open that Media item's detail page.

#### Scenario: Lead person with other titles

- **WHEN** a Media item's first person credit is credited on two other Media
- **THEN** the page shows a "More with" row named for that person with those two cards and not the
  current item

#### Scenario: Lead person with no other titles

- **WHEN** the first person credit is credited only on the current item
- **THEN** the page shows no "More with" row

### Requirement: Related row

The web Media detail page SHALL show a Related row listing Media that share at least one
category-archetype credit with the item, ordered by the number of the item's category credits and
first three person credits they share, most first, ties in the server's order. The row SHALL NOT
list the current item, and SHALL NOT be shown when no Media qualify.

#### Scenario: Ranked by shared credits

- **WHEN** one Media shares two of the item's category credits and another shares one
- **THEN** the Related row lists the first before the second, without the current item

#### Scenario: No category credits

- **WHEN** the item has no category credits
- **THEN** the page shows no Related row
