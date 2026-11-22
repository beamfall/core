## MODIFIED Requirements

### Requirement: Play history is listed and filterable

The admin field `playHistory` SHALL list plays newest first, where a play is a run of playback
progress events for one profile, item and playback ID with no gap longer than 30 minutes, and SHALL
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

A playback progress report SHALL record the reporting session's device, the client's playback ID
when it sends one, whether the player said it was paused when it says so, and, when the server
holds a delivery decision for that session and item, its play method.

#### Scenario: Transcoded session

- **WHEN** a player whose session the server is transcoding reports progress
- **THEN** the resulting play lists that device and the method `transcode`

### Requirement: Both report APIs accept the playback ID as sent

GraphQL `reportPlayback` and `PUT /v1/media/{mediaID}/progress` SHALL accept the playback ID and
paused state alike. A playback ID SHALL be checked exactly as sent, never trimmed: one longer than
64 characters (Unicode code points) or with leading or trailing whitespace SHALL be refused.

#### Scenario: Long and padded IDs

- **WHEN** a client sends a playback ID of 64 accented letters, one of 65 letters, or one with a
  leading space
- **THEN** the first is recorded and the other two are refused

#### Scenario: Playback ID through the device API

- **WHEN** a device reports progress through `/v1` with a playback ID
- **THEN** the progress event records that ID, and an ID with surrounding whitespace is refused

### Requirement: Clients name each play with a playback ID

A client SHALL send a new random playback ID each time its player starts an item and keep it
across pauses, seeks and reloads of the same item. Playing an item again after its finish SHALL
start a new playback ID.

#### Scenario: Replay in place

- **WHEN** an item plays to its finish and the viewer presses Play again on the same player
- **THEN** the second play's reports carry a new playback ID and its heartbeat runs again

### Requirement: Clients send a play's reports in order

A client SHALL send the reports for one playback ID one at a time in the order it made them, never
dropping or reordering one. Only a periodic heartbeat MAY replace the heartbeat queued just before
it in the same state. A reload of the same item SHALL send the reports still queued for its
playback ID before any of its own.

#### Scenario: Pause, resume and heartbeat behind a stalled report

- **WHEN** a playing report stalls and the viewer pauses, resumes and a heartbeat follows
- **THEN** all four reports are sent in that order, with one playback ID

#### Scenario: Quality change behind a stalled report

- **WHEN** a playing report stalls, the viewer pauses and seeks while paused, then changes quality
- **THEN** the pause and the seek are sent in order, before the reloaded stream's reports, with one
  playback ID

#### Scenario: Auto-advance with reports queued

- **WHEN** the player moves to the next item while reports and the finish are queued
- **THEN** the queued reports are sent in order and the finish last

### Requirement: Client event times only move forward

A client's event times for one playback ID SHALL come from a clock that only moves forward, such as
the wall clock at the play's start carried forward by a monotonic timer, so a device clock change
mid-play neither stalls nor rewinds them.

#### Scenario: Device clock set back mid-play

- **WHEN** the device clock is set back two minutes while an item plays for three seconds
- **THEN** the play's next report carries an event time three seconds after its previous one

### Requirement: A play is the reports sharing a playback ID

Reports with the same playback ID SHALL form one play, ordered by server receipt and split only by
silence longer than 30 minutes. Reports with different playback IDs SHALL never join one play.
Reports without a playback ID SHALL be grouped and credited exactly as before this change.

#### Scenario: Concurrent devices

- **WHEN** a profile plays one item on two devices at the same time and their reports interleave
- **THEN** two plays are listed, one per device, each with its own watched time

#### Scenario: Short hand-off

- **WHEN** a TV plays 60 seconds, a phone 45 and the TV resumes 50 seconds after its last report
  for 60 more, each with its own playback ID
- **THEN** three plays are listed, totalling 165 watched seconds

#### Scenario: Reports without a playback ID

- **WHEN** an older client reports a minute each on a TV, a phone, the TV and the phone
- **THEN** one play of 240 watched seconds is listed, as before this change

### Requirement: A play's method covers most of its watched time

Each stretch of watched time SHALL count toward the method on the report that ends it, and a
play's method SHALL be the method with the most watched time, ties going to the most demanding of
transcode, remux and direct. A method with no watched time SHALL never win while another has some.
A play with no watched time SHALL take the most demanding method recorded, or `unknown` when none
is.

#### Scenario: Delayed report or slow client clock

- **WHEN** a report arrives late, or the client's clock runs behind the server's
- **THEN** the play keeps the method of the stream decision that covered its playing time

#### Scenario: Trailing transcode report

- **WHEN** a play direct-plays for ten minutes and its last report, with no playing time after the
  one before it, says transcode
- **THEN** the play's method is direct and transcode is credited 0 seconds

### Requirement: Watched time counts playing time only

Within a play, each step between consecutive reports SHALL credit the position advance capped at
the time between them, and nothing when the earlier report said paused or the advance is negative.
The time is the client's clock difference when both reports carry one and it moved forward, else
the receipt difference for that step. A report that does not say whether it was paused SHALL count
as playing. No cap of a minute applies.

#### Scenario: Client clock runs fast

- **WHEN** a client's event times run ahead of real time
- **THEN** no step credits more than its position advance and no play more than the item's known
  duration; crediting by the client's clock keeps the time of reports queued behind a slow request,
  at the cost of trusting that clock within these bounds

#### Scenario: Paused seek

- **WHEN** a player reports paused, seeks 45 seconds ahead while paused and resumes
- **THEN** the seek adds no watched time

#### Scenario: Delayed report

- **WHEN** a TV's reports go from position 60 to 135 across a 75-second network stall
- **THEN** the TV's play still gains 180 watched seconds over 180 seconds of playing

#### Scenario: Reports queued behind a stalled one

- **WHEN** a pause, a resume and a heartbeat 14 seconds later queue behind a stalled report and
  arrive together
- **THEN** the play gains the 14 seconds; a paused seek to the end, play and finish queued the same
  way gain only the last second

#### Scenario: Client clock runs backward

- **WHEN** a client's clock jumps backward between two minutes of playing reports
- **THEN** the play still gains 120 watched seconds

#### Scenario: Client clock stands still

- **WHEN** a client sends the same event time for a minute of playing reports that arrive and
  advance normally
- **THEN** each of those steps credits the receipt difference, and the play gains the full time

#### Scenario: Upgraded history

- **WHEN** reports without a playback ID show a 20-minute pause between two reports
- **THEN** the pause adds no watched time, as before the upgrade
