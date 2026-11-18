# Hydration: one container per folder, title variants merged

Ticket: CORE-2459 (`queue:core:main`).

## Why

A TV library scan split one show folder into several Shows. The film and TV hydration mapper named
the series (and scoped each season) by the title parsed from each file name, so
`/tv/Gangs of London/Season 1/gangs.of.london.s01e01...mkv` and the Title Case files in the other
season folders became three Shows ("Gangs Of London", "gangs of london", "Gangs of London"), with
art and metadata split across them. Ted Lasso and Police Suspect No 1 split the same way.

## What Changes

- The film and TV mapper names a series from the enclosing show folder (up to three levels up,
  trailing year or id tags stripped) when that folder's name folds equal to the parsed title;
  otherwise it keeps the parsed title.
- A mapper may declare title-fold kinds (`HydrationTitleFoldMapper`). For those kinds, hydration
  merges Entities whose titles differ only in case, spacing or punctuation through the existing
  recoverable entity merge, after copying ratings, watchlist entries and tags to the survivor.
  It merges a Media's previous holder into its new one, merges every live fold-equal variant
  into the Entity a hydration names (checked once per Entity per process), and sends a file
  still spelling a merged-away title to the survivor.
- A one-time startup repair per plugin and kind reopens the Media of fold-equal Entities and
  queues the backfill scan, so libraries hydrated before the fix are merged by the next scan.

## Non-goals

- Encrypted Libraries: the entity merge journals in the shared catalog, so title folding stays
  off there and the repair covers the shared catalog only.
- Matching by provider metadata, and merging titles that differ by more than case, spacing or
  punctuation.

## Failure modes

- A failed merge fails that Media's hydration before its completion marker is written; the next
  scan retries it. Nothing is deleted: merged-away Entities go to trash.

## Acceptance evidence

- Mapper test with the Gangs of London folder shape (Season 1 dotted lower-case names, Season 02,
  Season 3) producing one series.
- Hydration tests: rescan merge with item state kept, failed-merge retry, undo restoring Media
  membership, a new spelling absorbing the only existing variant, and encrypted Libraries left
  alone.
- Tower check: Gangs of London, Ted Lasso and Police Suspect No 1 each listed once.

## Rollback

Revert the change. Merges already made can be undone per merge batch from the activity log, and
merged-away Entities can be restored from trash. No migration is added; the repair records its run
in `server_config`.

## Capabilities

### Modified Capabilities

- `hydration-entity-identity`: title-fold kinds, their merges and the one-time repair.
