## ADDED Requirements

### Requirement: Each Library has a server-side Home visibility setting

Every Library SHALL carry a persisted `showOnHome` setting. A Library SHALL be shown on Home by
default, including every Library that existed before the setting was introduced. The admin
`libraries` query SHALL return `showOnHome` for each Library.

#### Scenario: New Library is shown on Home

- **WHEN** a manager creates a Library
- **THEN** its `showOnHome` is true

#### Scenario: Existing Library after upgrade

- **WHEN** a server with existing Libraries applies the migration
- **THEN** every existing Library has `showOnHome` true

### Requirement: A manager can hide a Library from Home

The admin mutation `setLibraryShowOnHome(libraryID, enabled)` SHALL persist the setting and return
the Library's `LibraryInfo`. It SHALL require an Admin Session, SHALL reject an unknown Library and
SHALL write a `library.show_on_home` activity record for the Library.

#### Scenario: Viewer cannot change the setting

- **WHEN** a non-admin session calls `setLibraryShowOnHome`
- **THEN** the call is refused and the setting is unchanged

#### Scenario: Manager hides a Library

- **WHEN** an Admin Session calls `setLibraryShowOnHome` with `enabled: false`
- **THEN** the returned `LibraryInfo.showOnHome` is false, the value survives a restart and an
  activity record is written

### Requirement: Home omits hidden Libraries and nothing else does

An aggregate `sourceItems` query (no `libraryID`) that passes `home: true` SHALL return no items
from a Library whose `showOnHome` is false. Queries without `home: true`, and every query scoped to
a `libraryID`, SHALL be unaffected, so the Library stays reachable from navigation, its own page,
history and search. Clients SHALL pass `home: true` for the shelves they render on Home.

#### Scenario: Recently added on Home skips a hidden Library

- **WHEN** a Library is hidden from Home and a client requests `sourceItems(kind: "recently-added",
  home: true)`
- **THEN** no item from that Library is returned, and items from shown Libraries are

#### Scenario: Hidden Library stays browsable

- **WHEN** a Library is hidden from Home and a client requests its own rows with `libraryID`
- **THEN** its items are returned as before

#### Scenario: Other aggregate feeds are unaffected

- **WHEN** a Library is hidden from Home and a client requests an aggregate feed without `home`
- **THEN** items from that Library are still returned
