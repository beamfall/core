## ADDED Requirements

### Requirement: A pack carries every Part of the selected Version

The offline pack builder SHALL write one pack item per Part of each Media's selected Version, in
Part order, and each manifest item SHALL name its Part ordinal (omitted when 0). The builder SHALL
refuse a Media whose selected Version's Parts are not contiguous from 0.

#### Scenario: Multi-Part book

- **WHEN** a pack is built for one Media whose selected Version has Parts 0, 1 and 2
- **THEN** the pack holds three items for that Media with Part ordinals 0, 1 and 2

#### Scenario: Missing Part

- **WHEN** the selected Version has Parts 0 and 2 but no Part 1
- **THEN** the build is refused

### Requirement: The manifest carries chapter Markers

The manifest item for Part 0 of each Media SHALL carry that Media's chapter Markers (Markers with
no segment kind) with title, start and optional end seconds. A single-Part Media without chapter
Markers SHALL produce the same manifest bytes as before this change.

#### Scenario: Book with chapters

- **WHEN** a pack is built for a Media with two chapter Markers and an intro segment
- **THEN** the Part 0 manifest item lists the two chapters and not the intro

### Requirement: Part content route

The pack content route SHALL serve `/api/pack/content/<token>/<media_id>/<part>` for each packed
Part, and SHALL serve Part 0 when no part is given.

#### Scenario: Fetch a later Part

- **WHEN** a client fetches `/<media_id>/1` for a packed book with two Parts
- **THEN** the route serves the Part 1 ciphertext

### Requirement: Stale offline progress does not clobber newer progress

A playback progress write carrying an event time older than the stored row's event time SHALL NOT
move the stored position, for audio Media as for video.

#### Scenario: Offline replay after newer online progress

- **WHEN** progress at 600 s with event time T2 is stored and an offline replay at 120 s with an
  earlier event time T1 arrives
- **THEN** the stored position stays at 600 s
