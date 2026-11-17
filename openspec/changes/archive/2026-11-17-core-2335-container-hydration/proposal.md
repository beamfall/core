# Hydrate container Entities and per-item details from scraper lookups

Ticket: CORE-2335 (`queue:core:main`). Needs plugin-sdk with `ItemLookupTemplate` and
`EntityMap.Container` (change `container-hydration-and-item-lookup`).

## Why

A Media identified against a container-level record (a series) gets that record's summary and
year, and the container Entities the plugin's own mapper created (series, season) get no poster,
overview or year from it. The source has the item's own record and the containers' details, but
core cannot address them.

## What Changes

- The scrape engine substitutes `{sequence}` and `{index}` from the Media's parsed ordinal. A
  request template substitutes an unknown value as empty; an entity map whose path names an
  unknown value is skipped.
- After the main lookup, a scraper's `ItemLookupTemplate` is fetched with the same id and the
  ordinal, mapped through `ItemFieldMap` and `ItemArtworkMap`, and overrides the main document's
  values. A template naming an unknown ordinal skips the fetch; a failed fetch keeps the main
  mapping.
- A `Container` entity map hydrates the existing container Entity of its kind that holds the
  Media (the CORE-1488 container chain): fields merge into the Entity's existing fields and its
  title is kept; the poster applies like other hydrated Entity posters. It never creates an Entity.
- Identification passes the parsed ordinal: Apply and Lookup run the plugin parser on the
  Media's primary file, and auto-identify applies through Apply. The parser title still wins over the
  scraper title.

## Non-goals

- No wire change: the new values are declared Entity fields and the existing poster slot.
