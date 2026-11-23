# Search and filter play history

Ticket: CORE-3333 (`queue:core:main`). Native clients follow in CORE-3334 (Apple) and CORE-3335
(Android).

## Why

The web History page lists the acting Profile's recently played and most played Media, but a long
history can only be scrolled. There is no way to find a past play by name, look at one Library or
see what was played recently.

## What Changes

- `sourceItems` gains optional `playedAfter` and `playedBefore` arguments (RFC 3339; after
  inclusive, before exclusive) that bound the last play of the `recently-played` and `most-played`
  kinds. A malformed bound is a client error. Every other kind ignores them.
- For the same two kinds, the existing `query` argument now matches Media whose title, or the title
  of an Entity linked to it (an artist or album, for example), contains every term, folded for case
  and diacritics.
- The existing `libraryID` argument narrows the played kinds to one Library, which is how the page
  filters by media type without naming a plugin.
- The store applies all three before paging, so every page is full until the feed ends.
- The wire contract is published as 2.52.0 (additive, minor).
- The web History page adds a search box, Library chips (shown when more than one Library is
  visible) and last-played range chips (any time, today, past 7 days, past 30 days, past year).
  Both feeds follow the filters. New strings ship in every locale.
