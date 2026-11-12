## ADDED Requirements

### Requirement: Scoped hydrated Entities are identified by scope and title

Local-metadata hydration SHALL derive the id of an Entity that carries a scope from its Library,
kind, scope and title, and the id of an Entity without a scope from its Library, kind and title.
The scope SHALL NOT change the Entity's display title.

#### Scenario: Same title under two scopes

- **WHEN** two files hydrate an Entity of one kind with the same title and different scopes
- **THEN** two Entities exist, each holding only its own file

#### Scenario: Same title under one scope

- **WHEN** two files hydrate an Entity of one kind with the same title and the same scope
- **THEN** one Entity exists and holds both files

### Requirement: Albums are scoped by album artist

The music hydration mapper SHALL scope an album by its album artist, falling back to the track's
first credited artist when no album artist is known, and SHALL write the album-artist relation for
that scope.

#### Scenario: Two same-titled albums by different album artists

- **WHEN** tracks tagged with one album title and two different album artists are hydrated
- **THEN** the two albums are separate Entities

#### Scenario: Compilation with one album artist

- **WHEN** tracks by different artists share an album title and one album artist
- **THEN** they belong to one album Entity

### Requirement: Title-only Entities from before scoping are adopted, not duplicated

When no scoped Entity exists and a title-only Entity of the same kind and title does, hydration
SHALL keep using the title-only Entity if its scope relation points at nothing or only at the
scope being hydrated, and SHALL create the scoped Entity otherwise.

#### Scenario: Rescan of an album keyed before scoping

- **WHEN** a file of an album whose Entity was created under the title-only key is hydrated again
- **THEN** the file stays on that Entity and no second Entity is created

#### Scenario: Title-only Entity belongs to another scope

- **WHEN** a file is hydrated whose scope differs from the only scope the title-only Entity records
- **THEN** a scoped Entity is created for the file and the title-only Entity is unchanged

### Requirement: Title-only Entities carrying several scopes are split once

At startup Core SHALL, once per scoped kind, clear the scope relation rows of every title-only-era
Entity whose scope relation points at two or more Entities and clear the hydration marker of that
Entity's member Media, so the next scan re-hydrates them under scoped identity. It SHALL NOT repeat
the split after it has run.

#### Scenario: Two albums merged by the old key

- **WHEN** an Entity records two scopes and the one-time split runs
- **THEN** the Entity records no scope, its member Media lack the hydration marker, and a second
  run changes nothing
