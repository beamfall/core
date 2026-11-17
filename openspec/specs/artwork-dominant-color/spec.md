# artwork-dominant-color Specification

## Purpose
The dominant colour the server reports for each artwork so clients can tint surfaces before the image loads.

## Requirements

### Requirement: An item's artwork carries its poster's dominant colour

Core SHALL derive a dominant colour from an item's stored poster and SHALL return it as
`dominantColor` on the item's `artwork` object, as a lowercase `#rrggbb` string. An item with no
poster, or with a poster Core cannot decode, SHALL omit the field.

#### Scenario: Item with a poster

- **WHEN** an item has a stored poster that is mostly one colour
- **THEN** its `artwork.dominantColor` is that colour as `#rrggbb`

#### Scenario: Item without a poster

- **WHEN** an item has no stored poster
- **THEN** it has no `artwork` object and no `dominantColor`

#### Scenario: Undecodable poster

- **WHEN** an item's stored poster bytes are not a decodable image
- **THEN** its `artwork` carries `poster` and omits `dominantColor`

### Requirement: The dominant colour follows the poster

The colour SHALL be stored beside the poster file, bound to the poster's content version, with no
database schema change. Importing a poster SHALL compute its colour. A colour recorded for
different poster bytes SHALL NOT be returned; Core SHALL recompute it from the current poster.

#### Scenario: Poster replaced

- **WHEN** an item's poster is replaced by an image of a different colour
- **THEN** the item's `artwork.dominantColor` is the new image's colour

#### Scenario: Poster stored before this change

- **WHEN** an item's poster was stored before colours were derived
- **THEN** the artwork sweep, or the first read of the item, records its colour
