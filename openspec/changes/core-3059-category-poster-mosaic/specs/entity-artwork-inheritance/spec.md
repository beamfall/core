## MODIFIED Requirements

### Requirement: Declared Entity kinds inherit a poster from their sources

For an Entity kind with a declared inheritance rule, the artwork sweep SHALL give an Entity that
has no poster a copy of the first poster found among the rule's sources, in declared order: member
Media through the named relation in relation order, or Entities of the named kind that point at it
through the named relation. A server-generated fallback poster SHALL NOT be inherited. An Entity
kind without a rule, declared or derived, SHALL NOT inherit a poster.

#### Scenario: Album takes its cover from a member track

- **WHEN** an album Entity has no poster and one of its tracks has one
- **THEN** after the sweep the album serves that poster

#### Scenario: Artist falls back to an album cover

- **WHEN** an artist Entity has no poster and an album that names it as album artist has one
- **THEN** after the sweep the artist serves that album's poster

#### Scenario: Only a frame grab is available

- **WHEN** the only poster among an Entity's sources is a server-generated fallback
- **THEN** the sweep writes no poster for the Entity

#### Scenario: Kind without a rule

- **WHEN** an Entity of a non-category kind with no inheritance rule has Media with posters
- **THEN** the sweep writes no poster for it

## ADDED Requirements

### Requirement: Category Entities inherit a poster mosaic

Every category-archetype Entity kind without a declared rule SHALL get a derived mosaic rule whose
sources are the container kinds that point at it, then its own Media relations. For a mosaic rule
the sweep SHALL compose the first four distinct source posters into one 2x2 inherited poster, and
SHALL inherit a single poster when fewer than four are distinct. A sweep whose tiles are unchanged
SHALL NOT rewrite the mosaic.

#### Scenario: Grouping with four distinct covers

- **WHEN** a category Entity groups Media and containers with at least four distinct posters
- **THEN** after the sweep it serves a 2x2 mosaic of the first four, marked inherited

#### Scenario: Members share one cover

- **WHEN** a category Entity's sources offer fewer than four distinct posters
- **THEN** after the sweep it serves a copy of the first

#### Scenario: Declared rule wins

- **WHEN** a plugin declares an inheritance rule for a category kind
- **THEN** the sweep uses that rule and derives none for the kind
