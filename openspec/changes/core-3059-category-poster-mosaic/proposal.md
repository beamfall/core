# Grouping Entities derive a poster mosaic

Ticket: CORE-3059 (`queue:core:main`).

## Why

Entities of a category-archetype kind (a plugin's genres or labels, for example) have no artwork:
providers supply none and no plugin declares an inheritance rule for them. Home and browse cards
show a placeholder with an unplayed badge, which looks broken on a TV. The members they group
already have posters.

## What Changes

- An `EntityPosterRule` can ask for a mosaic. The artwork sweep then composes the first four
  distinct source posters, in source order, into one 2x2 JPEG poster (800x1200, dark gutters) and
  stores it as an inherited poster. Posters with the same byte size count as one, so members that
  carry copies of one container's poster contribute one tile.
- With fewer than four distinct posters, or a tile that cannot be decoded, the Entity inherits a
  single poster as before.
- The marker records every tile's source, size and modification time; a sweep whose tiles are
  unchanged writes nothing, and a changed tile recomposes the mosaic.
- At startup every activated bundle's category-archetype kind without a declared rule gets a
  derived mosaic rule: first the container kinds that point at it, then its own Media relations.
  Conformance-seeded servers keep declared rules only, so the golden corpus stays byte-stable.
- Provider and local artwork keep winning over an inherited mosaic.

## Impact

- Code: `internal/artwork` (mosaic composition, marker tiles), `internal/app` (derived rules and
  wiring).
- No plugin SDK, store, migration, GraphQL or wire-contract change. Clients already render Entity
  posters, so web, Apple and Android cards pick the mosaic up unchanged.
- Entity posters are shared across Profiles; a mosaic can show a member cover a ceiling-limited
  Profile cannot open. Recorded on the ticket as a follow-up risk.
