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

### Requirement: Overlay slots keep transparency

Core SHALL store the overlay slots `logo`, `clearart` and `disc` as PNG with their alpha channel,
SHALL generate every derivative bucket of those slots as PNG with alpha, and SHALL serve them with
content type `image/png`. Every other slot SHALL stay JPEG and be served as `image/jpeg`.

#### Scenario: Transparent logo at every bucket

- **WHEN** a `logo`, `clearart` or `disc` image with transparent pixels is imported
- **THEN** the artwork route serves it at full size and at every bucket as `image/png`
- **AND** the pixels that were transparent in the source are transparent in each response

#### Scenario: Opaque slots are unchanged

- **WHEN** a `poster`, `backdrop`, `thumb`, `banner` or `season-poster` is imported
- **THEN** it is stored and served as `image/jpeg` exactly as before

### Requirement: Overlays stored before PNG storage stay served

An overlay slot that was stored as JPEG before overlay slots became PNG SHALL keep being served,
with its buckets, version and lock, until that slot is imported again. The next import SHALL store
the PNG and remove the JPEG. No migration or bulk rewrite SHALL run.

#### Scenario: Old JPEG logo

- **WHEN** an item holds only a JPEG `logo` from before this change
- **THEN** the artwork route serves it as `image/jpeg` and the item's `artworkSlots` lists `logo`

#### Scenario: Old JPEG logo is replaced

- **WHEN** a new `logo` is imported for that item
- **THEN** the route serves the new PNG and the old JPEG file is gone
