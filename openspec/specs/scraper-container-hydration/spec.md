# scraper-container-hydration Specification

## Purpose
How a plugin's scraper lookups fill the container Entities (an outer container and its ordered
children) that a Media belongs to, using the ordinal parsed from the Media's path.

## Requirements

### Requirement: Lookups carry the item's parsed ordinal

The scrape engine SHALL substitute `{sequence}` and `{index}` with the identified Media's parsed
sequence and index, a numeric sequence without leading zeros. A request template SHALL substitute
an unknown value as the empty string. An entity map whose path names an unknown value SHALL be
skipped.

#### Scenario: Season selected by the parsed sequence

- **WHEN** an entity map path is `seasons[season_number={sequence}]` and the Media parsed sequence
  `01`
- **THEN** the map resolves the element whose `season_number` is 1

### Requirement: A per-item lookup refines the identification

When a scraper declares `ItemLookupTemplate` and the Media's ordinal is known, identification SHALL
fetch the item document with the same id and map it with `ItemFieldMap` and `ItemArtworkMap`. Item
values SHALL override the main document's for the same field or artwork slot. A failed item fetch
SHALL keep the main mapping, and the plugin parser's title SHALL still win over the scraper title.

#### Scenario: Item year and summary

- **WHEN** the main document has `first_air_date` 2008 and the item document has `air_date` 2010
  and its own overview
- **THEN** the Media's year is 2010 and its summary is the item overview

### Requirement: Container maps hydrate existing container Entities

A `Container` entity map SHALL hydrate the existing container Entity of its kind that holds the
identified Media, directly or up the container chain inside the Media's Library. Its fields SHALL
merge into the Entity's existing fields, the Entity's title SHALL be kept, and its poster SHALL be
applied like other hydrated Entity posters. A container map SHALL NOT create an Entity.

#### Scenario: Series and season details

- **WHEN** a Media held by a season Entity inside a series Entity is identified with container
  maps for both kinds
- **THEN** both Entities carry the mapped overview and date fields and the mapped posters

#### Scenario: No container holds the Media

- **WHEN** no Entity of the map's kind holds the Media
- **THEN** nothing is hydrated for that map and the identification still applies
