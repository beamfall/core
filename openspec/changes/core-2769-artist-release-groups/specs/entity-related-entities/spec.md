## ADDED Requirements

### Requirement: A plugin can split a related kind into sections by a field

Core SHALL let plugin glue register a rule naming a kind, a text field the kind declares and an
ordered list of field values with display names, and SHALL ignore a rule whose field the kind does
not declare as text. Core SHALL NOT name any kind or value itself.

#### Scenario: A rule for an undeclared field

- **WHEN** a rule names a field the kind does not declare
- **THEN** the rule is dropped and the group carries no sections

### Requirement: A ruled kind is ordered by value and lists its sections

For a ruled kind, the `entity(id)` read SHALL return one group ordered by rule value (items without
a value first, unlisted values last), each run newest first with undated items last, and whose
`sections` list each run's value, display name and item count in order.

#### Scenario: Albums grouped by release type

- **WHEN** an artist has two albums and one EP, the albums from 1999 and 2010, and the album kind is
  ruled by its release-type field with values album then ep
- **THEN** the artist's read lists one album group ordered 2010 album, 1999 album, EP, with sections
  album (2) and ep (1)

### Requirement: Related Entity cards carry a year

Each related Entity card SHALL carry the earliest year of its directly linked visible Media, when
any has one.

#### Scenario: An album card shows its year

- **WHEN** an album's tracks are from 2005
- **THEN** the album's card in its artist's read carries year 2005
