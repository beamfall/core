# Artwork slot vocabulary

Ticket: CORE-0394 (`queue:core:main`, ART-SLOTS-1).

## Why

Core knows three artwork slots (`poster`, `backdrop`, `thumb`). The plugin SDK (v0.3.2, already
pinned) accepts `logo`, `portrait`, `banner`, `clearart`, `disc` and `season-poster` in a
scraper's artwork map, and plugins already emit some of them, but identification iterates only
Core's three slots, so the rest are dropped silently. The tvOS redesign needs title logos and
per-container posters.

## What Changes

- Core's artwork slot vocabulary gains `logo`, `banner`, `clearart`, `disc` and `season-poster`.
  Identification downloads and stores them like `backdrop` and `thumb`, the artwork route serves
  them with the same derivative buckets, and `artworkSlots` on Source items and Media reads lists
  them when stored. `artworkSlots` is an opaque map, so the wire schema does not change.
- `season-poster` falls back to `poster` at the artwork route, as `backdrop` and `thumb` do.
  `logo`, `banner`, `clearart` and `disc` have no fallback: a poster drawn as a logo or disc
  would render wrongly, so an absent slot serves nothing.
- `backdrop` stays one scalar slot. Ordered multiple backdrops are ART-BACKDROP-MULTI-1.
- `portrait` and any other slot outside Core's vocabulary stay ignored.

## Impact

`internal/artwork`, `internal/identify`, `internal/graph` (slot map), `internal/mediahttp`
(artwork route). No migration and no wire-contract change.
