## ADDED Requirements

### Requirement: Film and TV series are named by their folder

The film and TV hydration mapper SHALL take a series title from the nearest enclosing folder, up
to three levels above the file, whose name with trailing year or id tags removed folds equal to
the title parsed from the file name, and SHALL keep the parsed title when no folder matches.

#### Scenario: Season folders with differently spelled file names

- **WHEN** a series folder holds season folders whose files spell the series title in different
  case or punctuation
- **THEN** every file hydrates into one series Entity titled from the folder

### Requirement: Title variants of a fold kind are merged recoverably

For a kind a mapper declares as title-fold, hydration SHALL treat Entities of the kind in one
Library whose titles fold equal (letters and digits only, lower case), and for a scoped kind share
the scope Entity, as one item. It SHALL merge them through the recoverable entity merge after
copying ratings, watchlist entries and tags to the surviving Entity, and SHALL send a file still
spelling a merged-away title to the surviving Entity.

#### Scenario: Rescan of a split library

- **WHEN** the Media of two fold-equal Entities are hydrated again under one spelling
- **THEN** one Entity holds them all and keeps the ratings recorded against either, and the other
  is in trash

#### Scenario: A new spelling of an existing item

- **WHEN** a file spells an existing Entity's title differently but fold-equal
- **THEN** the existing Entity merges into the Entity the file names, with its Media

#### Scenario: Undo a merge

- **WHEN** a title-variant merge is undone
- **THEN** each Entity holds the Media it held before the merge, including a newly hydrated one

### Requirement: A failed title-variant merge is retried

When a title-variant merge fails, hydration SHALL fail for that Media before its hydration marker
is written, so the next scan hydrates it again and completes the merge.

#### Scenario: A merge fails

- **WHEN** a title-variant merge fails during hydration
- **THEN** the Media stays unmarked and the next hydration completes the merge

### Requirement: Encrypted Libraries are not title-folded

Hydration SHALL NOT merge title variants in an encrypted Library, whose Entities the shared-catalog
merge journal cannot reference.

#### Scenario: Encrypted Library

- **WHEN** a Media in an encrypted Library is hydrated
- **THEN** no title-variant merge runs for it

### Requirement: Libraries split before title folding are repaired once

At startup Core SHALL, once per plugin and title-fold kind, clear the hydration marker of the Media
related to every set of two or more live fold-equal Entities in a shared-catalog Library, and SHALL
queue the backfill scan of each Library it reopened.

#### Scenario: Startup after upgrade

- **WHEN** a Library holds "Gangs Of London" and "gangs of london" as separate Entities and Core
  starts
- **THEN** their Media lack the hydration marker, a backfill scan is queued, and a second start
  changes nothing
