## ADDED Requirements

### Requirement: Listed Entities carry per-kind child counts

`libraryEntities` SHALL answer `childKinds`, one entry per child kind of the listed kind with its
plugin-declared `kindDisplayName` and `kindPluralDisplayName`, and each listed Entity SHALL carry
`childCounts`, the number of its visible linked Entities of each child kind with at least one, in
`childKinds` order. `childCount` SHALL equal the sum of `childCounts`. Core SHALL name no kind.

#### Scenario: Artist with albums

- **WHEN** a music Library lists artists and an artist links four visible albums
- **THEN** `childKinds` names the album kind with the plugin's "Album" and "Albums", and that
  artist's `childCounts` is `[{kind: album, count: 4}]` with `childCount` 4

#### Scenario: No child kinds

- **WHEN** no declared kind relates to the listed kind
- **THEN** `childKinds` is empty and every item's `childCounts` is empty

### Requirement: Web Entity card names child counts in the plugin's words

The web Library kind view SHALL label an Entity card with no parents by its child counts, each
with the kind's plugin display name in singular or plural form for the viewer's locale, and SHALL
fall back to the Media count when the Entity has no child counts.

#### Scenario: Plural and singular

- **WHEN** one artist has four albums and another has one
- **THEN** their lines read "4 Albums" and "1 Album"

#### Scenario: No children

- **WHEN** a listed Entity has no child counts and three Media
- **THEN** its line reads the Media count, as before
