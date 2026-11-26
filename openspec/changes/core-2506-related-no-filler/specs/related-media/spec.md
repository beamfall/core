## MODIFIED Requirements

### Requirement: Related Media read

The graph SHALL answer `relatedMedia(mediaID, limit)` with Media that share at least two of the
named Media's first four category-archetype credits and first three person-archetype credits (in the
server's credit order), at least one of them a category credit, ordered by how many of those credits
they share, most first, ties in first-read order. The limit SHALL default to 20 and be capped at 50.

#### Scenario: Exclusions and duplicates

- **WHEN** related Media are listed
- **THEN** the named Media is not listed
- **AND** Extras (the Extra bit, or the Extra side of an `extra-of` relation) are not listed
- **AND** Media with the same title (case and surrounding space ignored) and year are listed once, as
  the first-ranked of them, and Media with the named Media's own title and year are not listed
- **AND** Media that share only one credit are not listed, so the result MAY be shorter than the
  limit or empty

#### Scenario: Ranked by shared credits

- **WHEN** one Media shares three of the item's credits and another shares two
- **THEN** `relatedMedia` lists the first before the second, without the item itself

#### Scenario: Second billed person counts

- **WHEN** the item's person credits are, in order, A, B, C and D and a Media shares a category
  and person B
- **THEN** that Media is listed

#### Scenario: No category credits

- **WHEN** the item has no category credits
- **THEN** `relatedMedia` returns an empty list

#### Scenario: One shared credit is filler

- **WHEN** a Media shares only one category credit with the item
- **THEN** `relatedMedia` does not list it, and returns an empty list when no Media shares two

#### Scenario: Extras left out

- **WHEN** a Media sharing the item's credits has the Extra bit or is an `extra-of` another Media
- **THEN** `relatedMedia` does not list it

#### Scenario: Same title listed once

- **WHEN** two Media with the same title and year both share the item's credits
- **THEN** `relatedMedia` lists only the first-ranked of them
