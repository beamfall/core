# artwork-slots Specification

## Purpose
Fix the artwork slots Core stores and serves, so plugins and clients share one small vocabulary and a scraper cannot add a slot.

## Requirements

### Requirement: Core artwork slot vocabulary

Core SHALL store and serve the artwork slots `poster`, `backdrop`, `thumb`, `logo`, `banner`,
`clearart`, `disc` and `season-poster`. Identification SHALL import a looked-up URL for each of
these slots, and SHALL ignore any other slot name a scraper returns.

#### Scenario: Scraper returns the widened slots

- **WHEN** a lookup result maps `logo`, `banner`, `clearart`, `disc` and `season-poster` to URLs
- **THEN** each slot's bytes are imported, served at the artwork route with every derivative
  bucket, and listed in the item's `artworkSlots`

#### Scenario: Slot outside the vocabulary

- **WHEN** a lookup result maps `portrait` to a URL
- **THEN** the URL is not fetched and the artwork route answers 404 for that slot

### Requirement: Per-slot artwork fallback

The artwork route SHALL serve a slot's own stored file when present. When it is absent,
`backdrop`, `thumb` and `season-poster` SHALL fall back to the stored `poster`; `logo`, `banner`,
`clearart` and `disc` SHALL have no fallback.

#### Scenario: Container poster absent

- **WHEN** an item has a poster and no `season-poster`
- **THEN** a request for `season-poster` serves the poster

#### Scenario: Logo absent

- **WHEN** an item has a poster and no `logo`
- **THEN** a request for `logo` serves nothing

#### Scenario: Imported slot wins over fallback

- **WHEN** an item has both a poster and an imported `season-poster`
- **THEN** a request for `season-poster` serves the imported file
