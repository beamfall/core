## MODIFIED Requirements

### Requirement: A player ends its own live session

The session mutation `endPlayback(mediaId: ID!): Boolean!` SHALL end the calling device's live
session for that item and return true. It SHALL return true when there is nothing to end. It
SHALL NOT end another device's session. It SHALL stop the calling session's transcode of that item
and SHALL leave the session's other items' transcodes running.

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

#### Scenario: Transcoding player closes

- **WHEN** a device that is transcoding an item calls `endPlayback` for it
- **THEN** that transcode stops and frees its slot without waiting for the idle reap
- **AND** the same device's transcode of another item keeps running

## ADDED Requirements

### Requirement: Revoking a device aborts its transcodes

`revokeDevice` and `revokeAllDevices` SHALL abort every in-flight transcode owned by any of the
revoked device's sessions, and SHALL report the count in `transcodesAborted`. They SHALL find those
sessions from the device's auth sessions as well as from the stream-token ledger, so a transcode
whose ledger rows `endPlayback` or grant expiry already cleared is still aborted. Other devices'
transcodes SHALL keep running.

#### Scenario: Transcode outlived its ledger rows

- **WHEN** a device's transcode is still running after `endPlayback` cleared its ledger rows
- **AND** an operator revokes that device
- **THEN** the transcode is aborted and `transcodesAborted` counts it
