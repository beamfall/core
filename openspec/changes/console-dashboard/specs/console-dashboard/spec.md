## ADDED Requirements

### Requirement: Live sessions report the play method and why

`liveSessions` SHALL report for each session a `playMethod` of `direct`, `remux` or `transcode`,
a `playReason` from a closed vocabulary and a `bitrate` in bits per second, taken from the most
recent playback decision the server made for that session and item.

#### Scenario: No decision held

- **WHEN** the server holds no playback decision for a live session
- **THEN** `playMethod` is `transcode` when an HLS grant is outstanding and `direct` otherwise,
  with an empty reason and a zero bitrate

#### Scenario: Reason vocabulary

- **WHEN** a session reports a `playReason`
- **THEN** it is empty or one of `container`, `video-codec`, `resolution`, `bitrate`,
  `video-profile`, `hdr`, `audio`, `subtitles`, `device-quirk`, `capacity`, `cached-rendition`
  or `quality-choice`

#### Scenario: Direct play

- **WHEN** a device whose capability profile accepts the item's container and codecs asks for
  playback info and the operator lists live sessions
- **THEN** the session reports `playMethod` `direct`, an empty `playReason` and the item's
  source bitrate

#### Scenario: Transcode because of the video codec

- **WHEN** a device that cannot decode the item's video codec asks for playback info
- **THEN** the session reports `playMethod` `transcode` and `playReason` `video-codec`

#### Scenario: Restricted item

- **WHEN** a session plays an item the operator has not revealed
- **THEN** the session row is present without a title, and reports no play reason or bitrate

### Requirement: Host load is sampled for at least one hour

The server SHALL sample host CPU utilisation, memory in use and total, and network bytes sent and
received per second at a fixed interval, and SHALL retain at least the last hour of samples in
memory. The admin field `hostMetrics` SHALL return the retained samples oldest first with the
sampling interval. Samples SHALL NOT be persisted or sent off the server.

#### Scenario: Reading the ring

- **WHEN** an Admin Session reads `hostMetrics` after the sampler has taken samples
- **THEN** the response carries the interval and the samples in time order

#### Scenario: Not an admin

- **WHEN** a caller without an Admin Session reads `hostMetrics`
- **THEN** the field is refused

### Requirement: Play history is listed and filterable

The admin field `playHistory` SHALL list plays newest first, where a play is a run of playback
progress events for one profile and item with no gap longer than 30 minutes, and SHALL accept
optional `profileID`, `libraryID`, `since` and `until` filters and a bounded `limit`. Each play
SHALL carry the profile, the item title and library when the operator may see the item, the start
time, the watched seconds and whether the item was finished.

#### Scenario: Filter by profile

- **WHEN** two profiles have plays and the operator lists history for one profile
- **THEN** only that profile's plays are returned

#### Scenario: Filter by date

- **WHEN** the operator lists history with `since` and `until`
- **THEN** only plays that started inside the range are returned

### Requirement: Top profiles and most-played items

The admin field `playStats` SHALL accept a window of 7, 30 or 90 days and SHALL return the
profiles ranked by play count with their watched seconds, and the items ranked by play count,
over that window. Any other window SHALL be refused.

#### Scenario: Thirty-day window

- **WHEN** the operator reads `playStats` for 30 days
- **THEN** plays older than 30 days are not counted

#### Scenario: Unsupported window

- **WHEN** the operator reads `playStats` for 14 days
- **THEN** the field returns an error
