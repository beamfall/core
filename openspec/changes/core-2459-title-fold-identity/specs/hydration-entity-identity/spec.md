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
