## ADDED Requirements

### Requirement: Related Media reads carry card fields

Each `mediaRelations` item SHALL carry the related Media's `artworkSlots`, `durationSeconds` and
`mediaClass`, each omitted when unknown, with the same values `media(id)` returns for that Media.

#### Scenario: Extra with a poster

- **WHEN** a client reads `mediaRelations` with relation `extra-of` for a parent whose Extra has a
  stored poster, a duration and a class
- **THEN** the item's `media` carries the versioned poster URL in `artworkSlots`, the duration and
  the class

### Requirement: Extras and Editions rows

The web Media detail page SHALL show the Media that are `extra-of` the item in an Extras row and the
Media that are `edition-of` the item in an Editions row. A row with no items SHALL NOT be shown.
Choosing a card SHALL play that Media item.

#### Scenario: Item with extras

- **WHEN** a Media item has two Extras and no editions
- **THEN** the page shows an Extras row with two cards, each playing its own item, and no Editions
  row

#### Scenario: Item without extras

- **WHEN** a Media item has no Extras and no editions
- **THEN** the page shows neither row
