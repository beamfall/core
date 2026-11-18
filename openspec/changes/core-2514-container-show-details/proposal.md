# Container maps: backdrop, matched title and genre and cast links

Tickets: CORE-2514 (P1), CORE-2513 (P3) (`queue:core:main`).

## Why

A series page renders the series Entity, but a Container entity map can give that Entity only a poster
and typed fields. The page therefore has no backdrop, and the genres and cast the same TMDB document
carries are linked only to each episode Media, so the hero shows no genre chips and no cast row
(CORE-2514). The series also keeps its folder spelling ("A Mothers Son", "90 Day Fiance") after a match,
because a container map keeps the Entity's title (CORE-2513).

## What Changes

- The scrape engine resolves three Container-map fields the plugin SDK adds: `ArtworkMap` (more artwork
  slots, composed like Media artwork), `TitlePath` (the matched display title) and `ContainerRelations`
  (link the container to Entities a sibling map hydrated in the same lookup).
- Identification applies them to the container Entity: non-poster slots are imported unless the slot
  exists, is locked or the Library is Sealed or Encrypted (the entity poster rules); a non-blank title
  replaces the display title while the Entity ID stays the same; and container → Entity relations are
  written with the sibling's sort order, only when the bundle declares that relation toward the
  sibling's kind.
- `artwork.ImportEntityArtwork` generalises `ImportEntityPoster` to any slot.

## Impact

- Code: `internal/scrape`, `internal/identify`, `internal/artwork`, tests.
- Depends on the plugin SDK release that adds the three fields; core repins it, then repins
  `plugin-filmtv` once filmtv declares them for `tmdb-tv`.
- No wire, schema or migration change. Entity IDs are unchanged.
