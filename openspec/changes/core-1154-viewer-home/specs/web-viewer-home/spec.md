## ADDED Requirements

### Requirement: Home presents library shelves

The desktop Viewer SHALL retain the manifest's Continue Watching shelf and
present recent additions in separate library shelves. It SHALL render a
nonempty shelf when the library is confirmed to be visible on Home and its
feed succeeds. Until CORE-1496 supplies scoped Home visibility, the Viewer
SHALL confirm visibility by finding a shared item in the scoped feed and the
bounded aggregate Home feed.

#### Scenario: Recently added items span libraries

- **WHEN** the scoped Home feeds for two libraries return recent items
- **THEN** each confirmed library has its own shelf, including scoped items
  outside the aggregate Home feed's first page
- **AND** libraries whose scoped Home feeds return no items have no shelf

#### Scenario: One library feed is unavailable

- **WHEN** a scoped feed fails with a connection error
- **THEN** successful, confirmed shelves remain visible

### Requirement: Home features available artwork

The desktop Viewer SHALL show a hero when an eligible recent item has loadable
artwork within the aggregate Home feed or a visible library shelf. Home card
frames SHALL follow the loaded artwork's intrinsic aspect ratio and use the
class-based fallback when artwork is absent or fails.

#### Scenario: Home has mixed artwork

- **WHEN** the shelves include square, landscape and portrait artwork
- **THEN** the Viewer displays each at its intrinsic aspect ratio

#### Scenario: No eligible artwork loads

- **WHEN** no recent item has loadable artwork for the hero
- **THEN** Home shows its available shelves without a hero
