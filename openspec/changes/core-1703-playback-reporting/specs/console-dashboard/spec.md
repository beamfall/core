## MODIFIED Requirements

### Requirement: Play history is listed and filterable

The admin field `playHistory` SHALL list plays newest first, where a play is a run of playback
progress events for one profile, item and stream with no gap longer than 30 minutes, and SHALL
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

A playback progress report SHALL record the reporting session's device, a stream key derived
one-way from the authenticated session (never the session credential itself), whether the player
said it was paused when it says so, and, when the server holds a delivery decision for that
session and item, its play method.

#### Scenario: Transcoded session

- **WHEN** a player whose session the server is transcoding reports progress
- **THEN** the resulting play lists that device and the method `transcode`

### Requirement: Plays are split per stream

Reports from different streams SHALL never join one play, and reports without a stream key SHALL be
split per device. A stream's report SHALL start a new play when the stream was silent longer than a
minute and the previous report for that profile and item came from another stream. Streams that
report within a minute of each other SHALL stay separate plays without splitting each other.

#### Scenario: Concurrent devices

- **WHEN** a profile plays one item on two devices at the same time and their reports interleave
- **THEN** two plays are listed, one per device, each with its own watched time

#### Scenario: Repeated hand-offs

- **WHEN** a profile plays an item for a minute each on a TV, a phone, the TV and the phone, each
  within 30 minutes
- **THEN** four plays are listed, each with 60 watched seconds

### Requirement: Reports are ordered by receipt

Reports SHALL be ordered by the time the server received them, never by the client's clock. Events
written before receipt times SHALL keep the order of their observed time.

#### Scenario: Client clock runs backward

- **WHEN** a client's clock jumps backward between two minutes of playing reports
- **THEN** the play still gains 120 watched seconds

### Requirement: A play's method covers most of its watched time

Each stretch of watched time SHALL count toward the method on the report that ends it, and a
play's method SHALL be the method with the most watched time, ties going to the most demanding of
transcode, remux and direct. A method with no watched time SHALL never win while another has some.
A play with no watched time SHALL take the most demanding method recorded, or `unknown` when none
is. The method SHALL never depend on the client's clock.

#### Scenario: Delayed report or slow client clock

- **WHEN** a report arrives late, or the client's clock runs behind the server's
- **THEN** the play keeps the method of the stream decision that covered its playing time

#### Scenario: Trailing transcode report

- **WHEN** a play direct-plays for ten minutes and its last report, with no playing time after the
  one before it, says transcode
- **THEN** the play's method is direct and transcode is credited 0 seconds

### Requirement: Watched time counts playing time only

A play's watched seconds SHALL be the sum, over each consecutive pair of its stream's reports, of
the position advance capped at the time between the server receiving the two reports, counting
nothing when the advance is negative or the earlier report said the player was paused. A report
that does not say whether the player was paused SHALL count as playing. Events written before
receipt times SHALL keep the earlier rule unchanged: no credit across a gap of more than a minute.

#### Scenario: Paused seek

- **WHEN** a player reports paused, seeks 45 seconds ahead while paused and resumes
- **THEN** the seek adds no watched time

#### Scenario: Pause report delayed behind a stalled one

- **WHEN** a player's pause report is queued behind a stalled report and the user then seeks
- **THEN** the pause report is still sent before the seek report and the seek adds no watched time

#### Scenario: Coalesced reports

- **WHEN** a player reports position 100 and, five minutes of playing later, position 400
- **THEN** the play gains 300 watched seconds

#### Scenario: Upgraded history

- **WHEN** events written before receipt times show a 20-minute pause between two reports
- **THEN** the pause adds no watched time, as before the upgrade
