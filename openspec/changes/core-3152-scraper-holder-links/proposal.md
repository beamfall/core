# Scraper-declared container membership follows the identification

Ticket: CORE-3152 (`queue:core:main`).

## Why

A plugin can now place an identified Media into a container Entity straight from its lookup
document: a non-container EntityMap whose kind is a container and whose relation is that kind's
single-holder Media relation (the Film & TV plugin uses it to group films of one TMDB collection).
Two gaps keep such a container wrong after the first identification:

- When a re-identification resolves no Entity for the map (the new match belongs to no container),
  `applyEntities` writes nothing and the old link stays, so the Media still sits in a container it
  left. A move to another container is already handled by the relation's single cardinality.
- The map's element (the container object in the document) carries no ordinal for the member, and a
  scalar node gets no sort order, so the container lists its members by title, not in the order they
  were released, and it never seeds play-next.

## What Changes

- A lookup's single-holder link is owned by the identification that declares its map. When the
  applying scraper declares a non-container EntityMap onto a container kind's single-holder relation
  and the lookup hydrates no Entity for it, the Media's existing rows under that relation are
  removed.
- Such a link without an ordinal from its map takes the identified Media's year as its sort order,
  so the container's members list (and play next) in release-year order. Members from one year
  keep the store's title order. Links with a map ordinal keep it.
- The store gains `RemoveMediaRelations(mediaID, names)`, deleting a Media's rows under the named
  relations. Identification uses it through an optional seam, so a store without it keeps today's
  behaviour.

## Non-goals

- Day-precision release order. The map element cannot reach the lookup document root and
  `OrdinalField` takes an integer, so a date-valued ordinal needs a plugin-sdk capability.
- Identity keyed by the provider's container id. Hydrated Entities stay keyed by
  (Library, kind, name).
- Many-cardinality links (genres, studios) keep today's add-only behaviour on re-identification.

## Impact

- Code: `internal/identify` (`applyEntities`), `internal/store` (`RemoveMediaRelations`).
- No migration, GraphQL or wire-contract change. No plugin bundle declares such a map before the
  Film & TV release that adds TMDB collections, so existing libraries are unaffected.
