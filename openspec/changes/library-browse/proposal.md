# Browse a whole library: count, sort, filter, view modes, Play all and Shuffle

Ticket: CORE-1190 (`queue:core:main`).

## Why

A library page in the web app shows only the Home hubs. There is no way to see every item in a
library, how many there are, or to sort, filter or play the set. "See all" is a plain grid of one
Source with no controls. Finding one title in a 5,000-item library means scrolling a rail or using
search.

The existing `sourceItems` read cannot back a full browse: it returns no total, has a fixed order
per Source kind, and has no watch-state, year or resolution predicate. A client cannot sort or
count a library it has only partly loaded, so these must be answered by the server.

## What Changes

- New Graph read `libraryItems(libraryID, sort, direction, watch, entityFilter, yearFrom, yearTo,
  quality, facetEntityKind, seed, limit, offset)` returning `{ total, items, facets }`. `items` are
  the same `SourceItem` cards `sourceItems` returns. `total` counts the whole filtered set.
  `facets` lists the entity titles, decades and quality tiers present in the library, so a client
  offers only filters that can match.
- Visibility is the all-items Source's: reveal state, library allow-list, certification ceiling,
  trash, extras and per-Profile Mute. A library outside the session's scope answers an empty result
  with empty facets.
- Sort keys: `title`, `year`, `added`, `duration`, `last-watched`, `random` (seeded, stable for one
  seed). `direction` is `asc` or `desc`. Items missing the sort key (no year, never watched) sort
  last in both directions.
- The web library page gains a Browse view: visible count, virtualized grid or list (remembered
  per library), sort and filters held in the URL, removable filter chips, and Play all / Shuffle
  over the current filtered set.
- Wire contract: additive (one new read field), published as a minor version.

## Non-goals

- The A–Z jump rail (CORE-1071) and browse tiles (CORE-1074).
- Saved filters or smart collections.
- Multi-select of filter values (one genre, one decade, one quality at a time).
- Native clients. They can adopt `libraryItems` later; nothing they use changes.

## Failure modes

- An unknown sort, direction, watch state or quality tier is a client error. Ignoring it would show
  a wider set than the one the client labels.
- The read merges watch state (events database) with the catalog in memory, so its cost grows with
  library size, not page size. Measured against a 10,000-item library before landing; a library far
  beyond that may need an index-backed path.
- The play queue is held by the client. If the server is unreachable when the queue needs its next
  page, playback stops at the last loaded item.
