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

GraphQL `reportPlayback` and `PUT /v1/media/{mediaID}/progress` SHALL accept the playback ID,
paused state and played time (`playedMS`) alike, and the `/v1` OpenAPI `ProgressRequest` schema
SHALL document all three. A negative played time SHALL be refused. A playback ID SHALL be checked
exactly as sent, never trimmed: one longer than 64 characters (Unicode code points) or with leading
or trailing whitespace SHALL be refused.

#### Scenario: Long and padded IDs

- **WHEN** a client sends a playback ID of 64 accented letters, one of 65 letters, or one with a
  leading space
- **THEN** the first is recorded and the other two are refused

#### Scenario: Playback ID through the device API

- **WHEN** a device reports progress through `/v1` with a playback ID and played time
- **THEN** the progress event records both, and an ID with surrounding whitespace or a negative
  played time is refused

### Requirement: Clients name each play with a playback ID

A client SHALL send a new random playback ID each time its player starts an item and keep it
across pauses, seeks and reloads of the same item. Playing an item again after its finish SHALL
start a new playback ID, whose reports start from a presented frame, never the previous play's
end position.

#### Scenario: Replay in place

- **WHEN** an item plays to its finish and the viewer presses Play again on the same player
- **THEN** the second play's reports carry a new playback ID, the first of them the start
  position once the seek back settles, and its heartbeat runs again

### Requirement: Clients send a play's reports in order

A client SHALL send the reports for one playback ID one at a time in the order it made them, never
dropping or reordering one. Only a periodic heartbeat MAY replace the heartbeat queued just before
it in the same state. A reload of the same item SHALL send the reports still queued for its
playback ID before any of its own, and a replay SHALL send nothing until the previous play's
queued reports and finish are sent.

#### Scenario: Pause, resume and heartbeat behind a stalled report

- **WHEN** a playing report stalls and the viewer pauses, resumes and a heartbeat follows
- **THEN** all four reports are sent in that order, with one playback ID

#### Scenario: Quality change behind a stalled report

- **WHEN** a playing report stalls, the viewer pauses and seeks while paused, then changes quality
- **THEN** the pause and the seek are sent in order, before the reloaded stream's reports, with one
  playback ID

#### Scenario: Replay behind a queued finish

- **WHEN** the finish waits behind a stalled report and the viewer presses Play again at once
- **THEN** the finish is sent before the replay's first report

#### Scenario: Auto-advance with reports queued

- **WHEN** the player moves to the next item while reports and the finish are queued
- **THEN** the queued reports are sent in order and the finish last

### Requirement: Clients never drop or resend a retired play's reports

Moving to another item or play SHALL never drop a report a play has queued, even while that play
still waits behind an earlier play's: its reports are sent once the earlier play is done. Only a
change of signed-in identity SHALL discard them. A report that fails SHALL be dropped, never sent
again, so a failed finish releases the replay and can never arrive after the replay's reports.

#### Scenario: Leaving a replay that waits

- **WHEN** a replay's first report waits behind the previous play's finish and the viewer moves to
  another item
- **THEN** the finish is sent, then the replay's report

#### Scenario: Failed finish

- **WHEN** the previous play's finish fails, for example with a 503
- **THEN** the replay's reports are sent and the finish is never sent again

### Requirement: Clients report a play's played time

A client SHALL send `playedMS`, the milliseconds its player has actually played under the current
playback ID, measured with a monotonic clock and counted only while media plays: not paused,
seeking, ended or waiting for data. It SHALL start from zero for each new playback ID and carry on
across reloads of the same play.

#### Scenario: Pause, seek and stall

- **WHEN** a player plays 10 seconds, pauses for a minute, plays 5, seeks far ahead, plays 4,
  waits 20 seconds for data and plays 1 more
- **THEN** its reports' played time grows to 20 seconds

### Requirement: Client event times never go backward

A client's event time SHALL be the wall clock, never before the last event time it sent, so a fast
clock that is corrected stops leading once the wall clock passes the last time sent. Event time
orders and dates reports; it SHALL NOT measure watched time.

#### Scenario: Fast clock corrected mid-play

- **WHEN** a play starts on a device clock two minutes fast, which is then corrected
- **THEN** the next report keeps the previous event time, and reports ten minutes later carry the
  wall clock

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

Within a play, a report carrying `playedMS` SHALL credit its growth over the largest earlier
`playedMS` in the play, taken in receipt order, and never less than zero. A report with a playback
ID but no `playedMS` SHALL credit the position advance capped at the receipt difference, and
nothing after a paused report. Reports without a playback ID SHALL be credited as before. A report
that does not say whether it was paused SHALL count as playing. No play SHALL exceed the item's
known duration.

#### Scenario: Client clock runs backward

- **WHEN** a client's clock jumps backward between two minutes of playing reports that arrive
  promptly
- **THEN** the play gains the 120 seconds its played time grew by

#### Scenario: Delayed seek

- **WHEN** a player seeks 75 seconds ahead and the report arrives after a 75-second network delay
- **THEN** the seek adds no watched time

#### Scenario: Reports queued behind a stalled one

- **WHEN** a pause, a resume and a heartbeat 14 seconds later queue behind a stalled report while
  the client's clock is set back, and arrive together
- **THEN** the play gains the 14 seconds; a paused seek to the end, play and finish queued the same
  way gain only the last second

#### Scenario: Repeated report

- **WHEN** a report's played time is sent again, or a later one arrives with a smaller count
- **THEN** that time is credited once

#### Scenario: Paused seek

- **WHEN** a player reports paused, seeks 45 seconds ahead while paused and resumes
- **THEN** the seek adds no watched time

#### Scenario: Upgraded history

- **WHEN** reports without a playback ID show a 20-minute pause between two reports
- **THEN** the pause adds no watched time, as before the upgrade

### Requirement: A play with a playback ID counts by its played time

Whether a play with a playback ID reaches the minimum span to count SHALL be decided by its
credited watched time, not by the distance between its first and last reports. Event time SHALL
only date the play.

#### Scenario: Left paused

- **WHEN** a play runs 10 seconds and is left paused, its paused reports running on for ten minutes
- **THEN** it is not listed as a play
