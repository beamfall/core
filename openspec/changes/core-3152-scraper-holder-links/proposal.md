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

- A lookup's single-holder link is owned by the scraper that wrote it. Every Entity→Media link a
  scraper writes records its writer (`scraper:<plugin>/<scraper>`) in a new nullable
  `entity_relation.writer` column; an operator's link, and every row written before this change,
  has no writer. When the applying scraper declares a non-container EntityMap onto a container
  kind's single-holder relation, the map ran, and the lookup hydrated no Entity for it, the Media's
  rows under that relation are removed, but only rows that scraper wrote from an Entity of the
  map's kind. An operator's link, another scraper's link and links from other kinds stay. A map the
  engine skipped (its path names a `{sequence}` or `{index}` the parser does not know) removes
  nothing under its kind and relation, even when a sibling map onto them resolved nothing: a skip
  says nothing about the document.
- Such a link without an ordinal from its map takes the Media's stored year, after field precedence,
  as its sort order, so the container's members list (and play next) in release-year order and a
  manual year outranks the scraper's. Members from one year keep the store's title order. Links
  with a map ordinal keep it.
- A plugin keys the container by the provider's container id, not its name: the link map's
  `NamePath` reads the id and the container map's existing `TitlePath` titles the Entity. A renamed
  container stays one Entity and two containers sharing a name stay two. The Entity is keyed by
  (Library, kind, name) and the name is the id. A link to an existing holder keeps its stored title
  (a document with an id but no name does not retitle it to the id; a new holder without a name
  falls back to the id), and the container map applies its title, fields and artwork only to a
  holder this lookup linked, so a document with a container name but no id leaves an operator's
  holder alone.
- The store gains `RemoveWrittenMediaRelations(mediaID, writer, relations)` and the scraper engine
  reports the entity maps it skipped (`scrape.Result.SkippedEntityMaps`). Identification removes
  links through an optional seam, so a store without it keeps today's behaviour.

## Design choice: link provenance

`entity_relation` had no provenance. The minimal addition is one nullable writer column (catalog
migration 0129, encrypted library 0012), with no index: only cleanup reads it, by target Media
through the existing index. An upsert sets the writer it is given, so an operator re-linking a row
takes it over. Rows from before the migration and rows an Entity merge copies have no writer, so
automated cleanup never removes them: the conservative side. The merge journal records each row's
writer and undo restores it; the journal is JSON, so this needs no migration. A scraper's move to another container
still replaces an operator's single-holder link through the relation's single cardinality, as it
did before; an operator relation lock is CORE-3131.

## Non-goals

- Day-precision release order. The map element cannot reach the lookup document root and
  `OrdinalField` takes an integer, so a date-valued ordinal needs a plugin-sdk capability (CORE-3156).
- Many-cardinality links (genres, studios) keep today's add-only behaviour on re-identification.

## Impact

- Code: `internal/identify` (`applyEntities`), `internal/scrape` (`SkippedEntityMaps`),
  `internal/store` (`RemoveWrittenMediaRelations`, the writer column).
- Migration: catalog 0129 and encrypted library 0012 add `entity_relation.writer`. No GraphQL or
  wire-contract change. No plugin bundle declares such a map before the Film & TV release that adds
  TMDB collections, so existing libraries are unaffected.
