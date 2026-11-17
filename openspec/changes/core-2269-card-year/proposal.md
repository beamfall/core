# A browse card carries its item's year

Ticket: CORE-2269 (`queue:core:main`)

## Why

A screen reader reads a browse card as one phrase: title, year, where the item sits in its parent,
how much is watched. Cards carry the title and the watch state today. The year is only on the
detail read, so a client would have to fetch every card's detail to speak it.

## What changes

- `SourceItem` gains `year`: the item's stored year as an integer, omitted when the item has none.
  Entity cards never carry it.
- Every read that returns cards fills it: `sourceItems`, `ambientQueue`, `libraryItems`, `search`,
  `searchEntities`, `mediaForEntity`, the entity page groups, the queue reads and `stationStart`.
- The year comes from the same per-card read that already sets `mediaClass`. `sourceItems` in
  semantic mode did not run that read, so those cards gain `mediaClass` as well and now match
  every other card.
- `searchResult` rows already carry `year` (CORE-1191) and are unchanged.
- Wire contract 2.17.0 carries the added field.

## Non-goals

- No `positionLabel`. Core holds no number it can show as a position: a relation's `sort_order` is
  an ordering key (a dated item stores its month and day there, and a scraped item falls back to
  its index in the source document), and no plugin declares a localized phrase to put a number
  in. Building the label needs a plugin-sdk declaration first; see `tasks.md`.
- No change to which items a session can see, or to `restricted`, `locked` and `muted`.

## Failure modes

- An item with no year, or an id the catalog no longer holds, omits `year`.
- An item in an open Encrypted Library is read from that library's own catalog, as `mediaClass`
  is. A sealed library's items are not returned at all, so they carry no year.
