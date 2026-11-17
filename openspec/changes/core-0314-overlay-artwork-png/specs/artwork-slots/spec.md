## ADDED Requirements

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
