## MODIFIED Requirements

### Requirement: Play history is listed and filterable

The admin field `playHistory` SHALL list plays newest first, where a play is a run of playback
progress events for one profile, item and device with no gap longer than 30 minutes, and SHALL
accept optional `profileID`, `libraryID`, `mediaID`, `deviceID`, `playMethod`, `since` and `until`
filters and a bounded `limit`. Each play SHALL carry the profile, the item title and library when
the operator may see the item, the device ID, name and client surface when known, the play method
(`direct`, `remux`, `transcode` or `unknown`), the start time, the watched seconds and whether the
item was finished.

#### Scenario: Filter by profile

- **WHEN** two profiles have plays and the operator lists history for one profile
- **THEN** only that profile's plays are returned

#### Scenario: Filter by date

- **WHEN** the operator lists history with `since` and `until`
- **THEN** only plays that started inside the range are returned

#### Scenario: Filter by device and method

- **WHEN** one profile plays an item direct on one device and transcoded on another, and the
  operator lists history with `playMethod` `transcode`
- **THEN** only the transcoded play is returned, carrying that device's name

### Requirement: Top profiles and most-played items

The admin field `playStats` SHALL accept either a window of 7, 30 or 90 days or a `since` instant
with an optional `until`, spanning at most 366 days, and the same `profileID`, `libraryID`,
`mediaID`, `deviceID` and `playMethod` filters as `playHistory`. It SHALL return the total plays and
watched seconds, the profiles ranked by play count with their watched seconds, the items ranked by
play count, the devices and play methods with their play counts and watched seconds, and the plays
and watched seconds per day in the caller's `utcOffsetMinutes`. Any other window, a range longer
than 366 days, or neither a window nor `since` SHALL be refused.

#### Scenario: Thirty-day window

- **WHEN** the operator reads `playStats` for 30 days
- **THEN** plays older than 30 days are not counted

#### Scenario: Unsupported window

- **WHEN** the operator reads `playStats` for 14 days
- **THEN** the field returns an error

#### Scenario: Breakdown by device and method

- **WHEN** plays come from two devices with different play methods over a date range
- **THEN** the devices and methods each list their own play counts and watched seconds, and the
  totals equal their sums

## ADDED Requirements

### Requirement: Progress reports record the device and play method

A playback progress report SHALL record the reporting session's device and, when the server holds
a delivery decision for that session and item, its play method. A device switch SHALL start a new
play. A play's method SHALL be the most demanding method recorded in it, in the order transcode,
remux, direct; a play with none recorded SHALL read as `unknown`.

#### Scenario: Transcoded session

- **WHEN** a player whose session the server is transcoding reports progress
- **THEN** the resulting play lists that device and the method `transcode`

#### Scenario: Device switch

- **WHEN** a profile plays an item on one device and continues it on another within 30 minutes
- **THEN** two plays are listed, one per device
