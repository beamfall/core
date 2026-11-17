# console-dashboard Specification

## Purpose
Let an owner see from the console what the server is doing and who is using it: live sessions with their playback decision, reason, bitrate and progress, graphs of host CPU, memory and network load, and play history with top users and most-played items.

## Requirements

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
  `policy-bitrate`, `video-profile`, `hdr`, `audio`, `subtitles`, `device-quirk`, `capacity`,
  `cached-rendition` or `quality-choice`
- **AND** `bitrate` means the client's own ceiling forced the transcode, while `policy-bitrate`
  means the admin's remote bitrate cap did

#### Scenario: Direct play

- **WHEN** a device whose capability profile accepts the item's container and codecs asks for
  playback info and the operator lists live sessions
- **THEN** the session reports `playMethod` `direct`, an empty `playReason` and the item's
  source bitrate

#### Scenario: Audio conversion is a transcode

- **WHEN** the server copies the video and converts the audio for a session
- **THEN** the session reports `playMethod` `transcode`, and `remux` only when the item is
  repackaged with no stream converted

#### Scenario: Transcode because of the video codec

- **WHEN** a device that cannot decode the item's video codec asks for playback info
- **THEN** the session reports `playMethod` `transcode` and `playReason` `video-codec`

#### Scenario: Restricted item

- **WHEN** a session plays an item the operator has not revealed
- **THEN** the session row is present without a title, and reports no play reason or bitrate

### Requirement: Host load is sampled for at least one hour

The server SHALL sample host CPU utilisation, host memory in use and total, and the server's own
network bytes sent and received per second at a fixed interval, and SHALL retain at least the last hour of samples in
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

### Requirement: Live sessions list only active playback

`liveSessions` SHALL leave out a playback whose player has not issued a stream token or reported
progress within the last two minutes. `transcoding` SHALL be true exactly when `playMethod` is
`transcode`.

#### Scenario: Player goes quiet

- **WHEN** a device's last token or progress report for an item is more than two minutes old
- **THEN** `liveSessions` does not list that playback

#### Scenario: Player reports again

- **WHEN** that device reports progress for the item
- **THEN** `liveSessions` lists the playback again

#### Scenario: Remux

- **WHEN** a live session's `playMethod` is `remux`
- **THEN** its `transcoding` is false

### Requirement: A player ends its own live session

The session mutation `endPlayback(mediaId: ID!): Boolean!` SHALL end the calling device's live
session for that item and return true. It SHALL return true when there is nothing to end. It
SHALL NOT end another device's session.

#### Scenario: Player closes

- **WHEN** a device calls `endPlayback` for the item it is playing
- **THEN** `liveSessions` no longer lists that device's playback of the item
- **AND** other devices' playbacks are still listed

#### Scenario: Repeated call

- **WHEN** a device calls `endPlayback` twice for the same item
- **THEN** both calls return true

#### Scenario: Missing item

- **WHEN** `mediaId` is empty
- **THEN** the call fails with a client error

### Requirement: A play lasts long enough and finishes near the end

A derived play SHALL last at least 30 seconds, or a quarter of the item's length when that is
shorter. A shorter run SHALL NOT be listed in `playHistory` or counted in the rankings. A play
SHALL be finished only when the item was reported watched and playback reached within 20% or 30
seconds of the item's end, measured by the highest reported position or the start position plus
the play's length. When the item's length is unknown, the watched report alone SHALL decide.

#### Scenario: Opened and closed

- **WHEN** an item is reported watched with no earlier report, or one second after the first
- **THEN** no play is listed

#### Scenario: Watched flag far from the end

- **WHEN** a 60-minute item plays for 10 minutes from the start and is then reported watched
- **THEN** the play is listed with 600 seconds and is not finished

#### Scenario: Resumed to the end

- **WHEN** a 60-minute item resumes at 40 minutes, plays for 20 minutes and is reported watched
- **THEN** the play is finished
