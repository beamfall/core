# Remove an item from Continue Watching and keep its progress

Ticket: CORE-2039 (`queue:core:main`). Clients: CORE-2017 (web card menu and Android long-press).

## Why

A viewer who abandons something half-way has no way to clear it from Continue Watching short of
marking it watched or unwatched, and both throw away the resume position. Plex lets a viewer remove
an item from the row while keeping its progress, and playing it again brings it back.

## What changes

- New `setContinueWatchingHidden(mediaID, hidden)` mutation for a session. `hidden: true` takes one
  visible Media off the calling Profile's `continue-watching` Source; `hidden: false` puts it back
  (the Undo path) in its original place. It returns the item's `ItemStateInfo` and emits the usual
  engagement invalidation so the Profile's other sessions refresh.
- The resume position, watched state and play count are untouched. The `resume-progress`
  "in-progress" filter still lists the item, and it still holds its container off Up Next.
- The next playback report that moves the position clears the removal, so playing the item again
  returns it to the row. A paused player repeating the same position does not.
- Store: events migration 0025 adds `playback_progress.continue_hidden`. The flag changes through a
  new `hide-continue` engagement event, so a projection rebuild reproduces it, and the existing
  prune, privacy-delete and merge paths that own the progress row carry it.
- Wire: one new mutation field (additive). Goldens are regenerated; publishing the next minor
  client-core-wire version is left to the lander.

## Non-goals

- Removing an Entity (a whole container) in one call. Clients remove the Media the card plays.
- A separate "hidden items" list or management screen.
