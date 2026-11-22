# Web: Remove from Continue Watching with Undo

Ticket: CORE-2017 (`queue:core:main`), web part. Server mutation: CORE-2039. Android long-press is
a separate change in beamfall-android.

## Why

A viewer cannot clear a half-watched item from Continue Watching on the web without marking it
watched or unwatched, which loses the resume position. Plex offers "Remove from Continue Watching"
on the card and keeps the progress.

## What changes

- A Media card on a Continue Watching shelf adds "Remove from Continue Watching" to its More menu.
  Cards on every other shelf, grid and page do not.
- Choosing it takes the card off the row at once and calls `setContinueWatchingHidden(hidden:
  true)`. The card stays off while a revalidation started before the write is in flight or the row
  is served from the viewer cache, until a fresh read of the row no longer lists it.
- The viewer's Undo notice (the one Hide uses) says the item was removed and offers Undo, which
  calls `setContinueWatchingHidden(hidden: false)` and puts the card back in its old place. A failed
  write puts the card back and the notice says it did not save.
- A row emptied by removals reads as empty to the Home layout, as a served empty row does.
- Progress is untouched; marking the item unwatched is still the way to clear it.
- New strings in the item-actions catalog for every shipped locale.

## Non-goals

- Removing from See All grids or the Media detail page.
- Android (beamfall-android).
