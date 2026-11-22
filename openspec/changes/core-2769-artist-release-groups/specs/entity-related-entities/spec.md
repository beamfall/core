## ADDED Requirements

### Requirement: A plugin can split a related kind into sections by a field

Core SHALL let plugin glue register a rule naming a kind, a text field the kind declares and an
ordered list of field values with display names, and SHALL ignore a rule whose field the kind does
not declare as text. Core SHALL NOT name any kind or value itself.

#### Scenario: A rule for an undeclared field

- **WHEN** a rule names a field the kind does not declare
- **THEN** the rule is dropped and the group carries no sections

### Requirement: A ruled kind is ordered by value and lists its sections

The `entity(id)` read SHALL apply sections independently to each owning Library's cards of a
ruled kind, ordered by rule value (no value first, unlisted values last) and newest first
within each run (undated last). Its `sections` list each run's value, display name and item
count. Unruled cards SHALL retain relation order and their owning plugin's archetype.

#### Scenario: Albums grouped by release type

- **WHEN** an artist has two albums and one EP, the albums from 1999 and 2010, and the album kind is
  ruled by its release-type field with values album then ep
- **THEN** the artist's read lists one album group ordered 2010 album, 1999 album, EP, with sections
  album (2) and ep (1)

### Requirement: Related Entity section values come from live Media

A ruled card's value SHALL be the most common non-empty field value on its currently linked
visible Media, with ties broken lexically.

#### Scenario: A track tag changes or a track moves

- **WHEN** a track moves to another album or its release-type tag is cleared
- **THEN** the next read uses the current links and tags, leaving an album untyped when no tagged track remains

### Requirement: Release-type backfill writes only the Media tag

Existing track Media SHALL receive the release-type field through a field-only backfill.

#### Scenario: Existing Media need the new field

- **WHEN** the release-type backfill visits an existing track
- **THEN** it uses a stored per-track value if available or reads the tag from the file
- **AND** it writes only the release-type Media field

### Requirement: Related Entity cards carry a year

Each related Entity card SHALL carry the earliest year of its directly linked visible Media, when
any has one.

#### Scenario: An album card shows its year

- **WHEN** an album's tracks are from 2005
- **THEN** the album's card in its artist's read carries year 2005
