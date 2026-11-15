# Audio placeholder for artwork-less audio cards

Ticket: CORE-1492 (`queue:core:main`).

## Why

A card or detail poster with no artwork draws a film glyph for every Media, so audio items look
like videos on shelves, in search, on entity pages and in library browse.

## What Changes

- The graph `SourceItem` gains an optional `mediaClass` (`audio`, `video`, `image`), filled for
  Media rows in one batch query per catalog. Entity rows leave it empty. This is an additive wire
  field (client-core-wire 2.8.0).
- Web cards and the Media detail poster draw an audio glyph when an artwork-less item reports class
  `audio`, and keep the film glyph otherwise.
- Web shelf, entity, search and library queries select `mediaClass`, so a play queue built from a
  shelf already knows each entry's class (CORE-1490's narrowing applies from the first click).
