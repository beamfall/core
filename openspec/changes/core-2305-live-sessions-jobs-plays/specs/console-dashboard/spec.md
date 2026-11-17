## ADDED Requirements

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
