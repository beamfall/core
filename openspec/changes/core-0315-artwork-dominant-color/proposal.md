# A poster's dominant colour on the item's artwork object

Ticket: CORE-0315 (`queue:core:main`)

## Why

Clients tint a surface behind an item (a focused card, a detail backdrop, a loading placeholder)
with a colour taken from its poster. Each client would otherwise decode the poster itself, and
before the poster has loaded it has nothing to show.

## What changes

- Core derives one dominant colour from a stored poster and keeps it in a small file beside the
  poster, bound to the poster's content version. No database column and no migration.
- The colour is computed when a poster is imported (scraped, local, manual pick, Entity poster),
  and the artwork sweep fills it in for posters that have none or whose poster changed. Reading
  an item whose poster has no current colour computes it once.
- `SourceItem.artwork` gains `dominantColor`: a lowercase `#rrggbb` string, omitted when the item
  has no poster or the poster cannot be decoded.
- Wire contract 2.16.0 (unpublished) carries the added field.

## Non-goals

- No palette, no per-slot colours, no colour for `backdrop` or other slots.
- No visual effect: the colour is metadata about a stored image. What a client draws with it is
  client work.

## Failure modes

- A poster Core cannot decode gets no colour; the failure is recorded against that poster version
  so it is not decoded again on every read.
- A colour file left from an older poster is ignored, because its recorded version no longer
  matches.
- Sealed and Encrypted Libraries hold no stored poster, so they get no colour.
