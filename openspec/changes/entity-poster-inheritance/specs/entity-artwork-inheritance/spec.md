## ADDED Requirements

### Requirement: Declared Entity kinds inherit a poster from their sources

For an Entity kind with a declared inheritance rule, the artwork sweep SHALL give an Entity that
has no poster a copy of the first poster found among the rule's sources, in declared order: member
Media through the named relation in relation order, or Entities of the named kind that point at it
through the named relation. An Entity kind without a rule SHALL NOT inherit a poster.

#### Scenario: Album takes its cover from a member track

- **WHEN** an album Entity has no poster and one of its tracks has one
- **THEN** after the sweep the album serves that poster

#### Scenario: Artist falls back to an album cover

- **WHEN** an artist Entity has no poster and an album that names it as album artist has one
- **THEN** after the sweep the artist serves that album's poster

#### Scenario: Kind without a rule

- **WHEN** an Entity of a kind with no inheritance rule has Media with posters
- **THEN** the sweep writes no poster for it

### Requirement: Provider artwork wins over an inherited poster

Importing an Entity poster SHALL replace an inherited poster and remove its inherited mark.
Identification SHALL import a provider poster for an Entity whose only poster is inherited, and
the sweep SHALL NOT replace a poster that is not marked inherited.

#### Scenario: Provider poster arrives after inheritance

- **WHEN** an Entity holds an inherited poster and identification supplies a provider poster
- **THEN** the Entity serves the provider poster and later sweeps leave it unchanged

### Requirement: Inheritance is idempotent and cheap across sweeps

A sweep over an Entity whose inherited poster's recorded source is unchanged SHALL NOT rewrite the
poster. The sweep SHALL replace an inherited poster only when its source poster changed or no
longer exists.

#### Scenario: Second sweep

- **WHEN** the sweep runs twice with no artwork change in between
- **THEN** the second run writes no Entity poster

#### Scenario: Source cover replaced

- **WHEN** the Media poster an Entity inherited from is replaced and the sweep runs
- **THEN** the Entity serves the new poster
