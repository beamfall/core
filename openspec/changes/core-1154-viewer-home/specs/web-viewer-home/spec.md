## ADDED Requirements

### Requirement: Home presents library shelves

The desktop Viewer SHALL retain the manifest's Continue Watching shelf and
present recent additions in separate library shelves. It SHALL render a
nonempty shelf when the library is returned by the `libraries` query, its
`showOnHome` field is anything other than `false`, and its scoped feed succeeds.

#### Scenario: Recently added items span libraries

- **WHEN** the scoped Home feeds for two libraries return recent items
- **THEN** each Home-visible library has its own shelf, including libraries
  with no items in the aggregate Home feed's first page
- **AND** libraries hidden from Home or whose scoped feeds return no items have no shelf

#### Scenario: One library feed is unavailable

- **WHEN** a scoped feed fails with a connection error
- **THEN** successful, Home-visible shelves remain visible

#### Scenario: Viewer session cannot read library visibility

- **WHEN** the `libraries` query requires an Admin Session
- **THEN** the Viewer retains the server-filtered aggregate Recently Added shelf

### Requirement: Home features available artwork

The desktop Viewer SHALL show a hero when an eligible recent item has loadable
artwork among the recent additions shown on Home. Home card
frames SHALL follow the loaded artwork's intrinsic aspect ratio and use the
class-based fallback when artwork is absent or fails.

#### Scenario: Home has mixed artwork

- **WHEN** the shelves include square, landscape and portrait artwork
- **THEN** the Viewer displays each at its intrinsic aspect ratio

#### Scenario: No eligible artwork loads

- **WHEN** no recent item has loadable artwork for the hero
- **THEN** Home shows its available shelves without a hero
