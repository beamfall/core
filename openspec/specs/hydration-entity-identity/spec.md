# hydration-entity-identity Specification

## Purpose
How hydration keeps scoped Entities distinct and repairs those created before scoping: a scoped Entity is identified by its scope and title, a title-only Entity from before scoping is adopted when it has exactly one scope and reopened once otherwise, and the repair queues the scan that hydrates what it reopened.

## Requirements

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

### Requirement: Title-only Entities without exactly one scope are reopened once

At startup Core SHALL, once per plugin and scoped kind, reopen every Entity of that kind in that
plugin's Libraries whose scope relation does not point at exactly one Entity: it SHALL clear that
Entity's scope relation rows and the hydration marker of its member Media, so they are
re-hydrated under scoped identity. It SHALL leave an Entity that records exactly one scope
unchanged, and SHALL NOT repeat the repair after it has run.

#### Scenario: Two albums merged by the old key

- **WHEN** an Entity records two scopes and the one-time repair runs
- **THEN** the Entity records no scope, its member Media lack the hydration marker, and a second
  run changes nothing

#### Scenario: Album written without an album artist

- **WHEN** an Entity records no scope and the one-time repair runs
- **THEN** its member Media lack the hydration marker and stay related to it

#### Scenario: The next scan after the repair

- **WHEN** the Media of a reopened Entity that held two scopes are hydrated again
- **THEN** the existing Entity holds one scope's Media, a new Entity holds the other's, and no Media
  is left without its Entity

### Requirement: The scope repair queues the scan that re-hydrates what it reopened

Because an ordinary scan does not hydrate an unchanged file again, Core SHALL queue a backfill scan
of every root of each Library whose Media the one-time repair reopened, and SHALL retry the
queueing at the next start when it fails.

#### Scenario: The repair queues the re-hydrating scan

- **WHEN** the one-time repair reopens Media in a Library
- **THEN** a backfill scan of each of that Library's roots is queued

#### Scenario: Queueing fails

- **WHEN** the backfill scan of a reopened Library cannot be queued
- **THEN** it is queued at the next start instead

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
the scope Entity, as one item, and SHALL merge newer variants into the oldest live one through the
recoverable entity merge, copying ratings, watchlist entries and tags to it first, a parent kind
before its children.

#### Scenario: Rescan of a split library

- **WHEN** the Media of two fold-equal Entities are hydrated again under one spelling
- **THEN** the oldest Entity holds them all, keeps its ID and the ratings recorded against either,
  the other is in trash, and activity history lists the merge batch

#### Scenario: Undo a merge

- **WHEN** the batches of title-variant merges listed in activity history are undone, newest first
- **THEN** each Entity holds the Media it held before the merges

### Requirement: The established title variant keeps its identity

For a title-fold kind, the oldest live variant SHALL keep its ID and title, and hydration SHALL
add a file naming any fold-equal variant to it.

#### Scenario: A new spelling of an existing item

- **WHEN** a file spells an existing Entity's title differently but fold-equal
- **THEN** the file joins the existing Entity, which keeps its ID and title, and nothing is merged

#### Scenario: A child only the newer parent spelling held

- **WHEN** two parent spellings each hold a different child and a scan merges the parents
- **THEN** both children live under the established parent with their own IDs, and no child is
  created twice

#### Scenario: The parent merge fails, then is undone

- **WHEN** the merge of the newer parent spelling fails during that scan, the next scan merges it,
  and the merge is then undone
- **THEN** no child is created twice or recorded under both parents, and after the undo each child
  is under its own parent spelling alone

#### Scenario: A newer child spelling also held by another parent

- **WHEN** a newer fold-equal child under the established parent is also recorded under an
  unrelated parent
- **THEN** it is not merged, and keeps its other parent and its Media

### Requirement: A title-variant merge keeps the merged variant's artwork

Before a title-variant merge commits, hydration SHALL copy each artwork slot of the merged-away
variant to the established one wherever the established variant has none of its own, including a
poster it only inherited, and the merged-away variant SHALL keep its own files for an undo.

#### Scenario: Only the newer spelling had a poster

- **WHEN** a scan merges a newer variant with a poster into an established variant without one
- **THEN** the established variant shows that poster, and undoing the merge restores the newer
  variant with its poster

#### Scenario: The copy stops part way

- **WHEN** a copy of the merged-away variant's artwork stops before it completes
- **THEN** the merge does not commit, and its retry finishes the copy with every mark of the slot

#### Scenario: The merged variant's artwork cannot be read

- **WHEN** the merged-away variant's artwork, or the legacy JPEG of an overlay slot, cannot be
  inspected, or the survivor's Library cannot be read
- **THEN** the merge does not commit, so the artwork is not left behind on a trashed variant

### Requirement: Automatic title-variant merges are recorded in activity history

Hydration SHALL record each title-variant merge it makes in activity history as
`entity.title_fold_merge`, with no actor, the Library, and the merge batch as its target. The merge
SHALL leave a record in the catalog that the entry is owed, and hydration SHALL write owed entries
before it merges again.

#### Scenario: A scan merges a parent and its child

- **WHEN** a scan merges a newer parent variant and its child into the established ones
- **THEN** activity history lists two `entity.title_fold_merge` entries, the child's newest, each
  naming the batch that undoes it

#### Scenario: The activity entry fails

- **WHEN** a merge commits but its activity entry cannot be written
- **THEN** that hydration fails, and the next hydration, in this process or after a restart, writes
  the entry with the merge's time

### Requirement: The title-variant lookup does not reread the kind

Finding the live title variants of an Entity SHALL NOT read every Entity of its kind in the Library
on each hydration: an unscoped lookup SHALL use an index of folded titles kept current with
written rows and checked against them, and a scoped lookup SHALL read only the scope's members.

#### Scenario: Many hydrations in one scan

- **WHEN** a scan hydrates many Media of one Library
- **THEN** the variant lookups read the kind once and afterwards only rows added since

### Requirement: A failed title-variant merge is retried

When a title-variant merge fails, hydration SHALL fail for that Media before its hydration marker
is written, SHALL clear a marker an earlier hydration wrote and SHALL queue a backfill scan of its
Library for the next start, so the Media is hydrated again and the merge completes.

#### Scenario: A merge fails

- **WHEN** a title-variant merge fails during hydration
- **THEN** the Media stays unmarked and the next hydration completes the merge

#### Scenario: A merge fails for a Media hydrated before

- **WHEN** a title-variant merge fails while a changed file whose Media carries its hydration marker
  is hydrated again
- **THEN** the marker is cleared and the Library awaits a backfill scan

### Requirement: A title-only Entity goes to one scope of a plan

When one hydration plan carries same-titled Entities of a scoped kind under different scopes and a
title-only Entity of that title records no scope yet, hydration SHALL keep the title-only Entity
for the first of those scopes and SHALL give each other scope its own scoped Entity.

#### Scenario: Two scopes in one plan

- **WHEN** a plan carries two same-titled albums under different album artists and a title-only
  album of that title records no album artist
- **THEN** the title-only album records the first album artist alone and the second gets its own
  album

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
