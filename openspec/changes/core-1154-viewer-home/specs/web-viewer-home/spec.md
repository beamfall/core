## ADDED Requirements

### Requirement: Home presents library shelves

The desktop Viewer SHALL retain the manifest's Continue Watching shelf and
present recent additions in separate library shelves. It SHALL render only
libraries whose scoped Home source returns items. The server SHALL return an
empty page for libraries hidden from Home when queried as a Home source.

#### Scenario: Recently added items span libraries

- **WHEN** the scoped Home feeds for two libraries return recent items
- **THEN** each library has its own shelf of recent items, even if its items
  are outside the aggregate Home feed's first page
- **AND** libraries whose scoped Home feeds return no items have no shelf

### Requirement: Home features available artwork

The desktop Viewer SHALL show a hero when an eligible recent item has loadable
artwork. Home card frames SHALL follow the loaded artwork's intrinsic aspect
ratio and use the class-based fallback when artwork is absent or fails.

#### Scenario: Home has mixed artwork

- **WHEN** the shelves include square, landscape and portrait artwork
- **THEN** the Viewer displays each at its intrinsic aspect ratio

#### Scenario: No eligible artwork loads

- **WHEN** no recent item has loadable artwork for the hero
- **THEN** Home shows its available shelves without a hero
