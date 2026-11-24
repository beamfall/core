# player-attribution Specification

## Purpose
Plays commanded on a shared Player display are credited to the household Profile that sent the command, so its watch state, resume position, history and scrobbles stay with that Profile, while walk-up and Ambient Display plays keep the Player Profile (ADR-0240).

## Requirements

### Requirement: Server-bound originator attribution on Player displays

When `controlRendererSession` accepts `play-media` for a `player` display, the server SHALL record
the caller's Profile and Session as originator and carry the originator's resume position when
`positionMs` is omitted. On `playbackInfo`, the server SHALL re-check becoming-current authorization
and bind originator and `player_binding_generation` to the issued `playback_grant`. Other
controllers' transport commands SHALL NOT change the originator.

#### Scenario: Play-media attributes playback to the originator across other controllers' transport

- **WHEN** controller A sends `play-media` to a Player display, controller B sends `pause` or `seek`,
  and the display fetches `playbackInfo` and reports progress and completion with its `grantID`
- **THEN** `playback_progress`, play history, and watched state land on controller A's Profile, and
  no engagement rows exist under the Player Profile

### Requirement: Idempotent and fenced display progress reporting

Progress and completion reports (`reportPlayback`, `PUT /v1/media/{mediaID}/progress`) SHALL
accept `grantID` and `sequence` and SHALL NOT accept a Profile ID from a display. Reports SHALL be
idempotent per `(grantID, sequence)` and SHALL be dropped when the grant has ended, was cancelled,
expired, or belongs to an older `player_binding_generation`.

#### Scenario: Duplicate, ended, or older-generation report is dropped

- **WHEN** a display retries a `(grantID, sequence)` report, reports on a grant after `finished`,
  `endPlayback`, or `stop`, or reports on a grant issued before a `SetDevicePlayerBinding`
  generation increment
- **THEN** the report is dropped as a no-op with no engagement or scrobble side effects

### Requirement: Originator consent, deletion, walk-up, and Player Profile isolation

Scrobbling for attributed display playback SHALL run under the originator Profile's consent
settings. If the originator Profile is deleted, a playing item finishes on its grant and records
nothing, while pending entries for it are skipped when becoming current. Walk-up plays and Ambient
Display on a Player display (both visual and audio axes) SHALL record no engagement, and core
engagement writers SHALL refuse to write under a `player` Profile ID.

#### Scenario: Deleted originator, walk-up, and Player Profile writer guard

- **WHEN** an originator Profile is deleted mid-playback or before becoming current, or a walk-up
  or Ambient Display play reports on a Player display, or `UpsertPlaybackProgress` is called with a
  `player` Profile ID
- **THEN** no engagement is recorded for the deleted originator or Player Profile, pending entries
  for the deleted originator fail becoming-current authorization, and `UpsertPlaybackProgress`
  rejects the `player` Profile ID
