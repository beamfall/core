## MODIFIED Requirements

### Requirement: Quality selection on direct play

The quality menu SHALL list the renditions of the HLS stream when the active stream is direct play.
When the player can renegotiate, selecting a rendition SHALL remember it as the quality ceiling and
re-request playback info at the current position under that ceiling. A player that cannot
renegotiate SHALL switch playback to HLS at the current position with that rendition pinned.

#### Scenario: Step down from direct play

- **WHEN** the viewer picks a rendition while direct play is active
- **THEN** the player re-requests playback info at the current position with that rendition's
  bandwidth as `maxStreamingBitrate`

#### Scenario: Step down without renegotiation

- **WHEN** the viewer picks a rendition while direct play is active and the player cannot
  renegotiate
- **THEN** the player attaches the HLS stream at the current position and pins that rendition

#### Scenario: Stream-token refresh after a later quality choice

- **WHEN** the viewer picked a rendition on direct play, then chose Auto or another rendition, and a
  stream-token refresh reloads the source
- **THEN** the reload keeps the viewer's current choice and never restores the first pick

#### Scenario: Stream-token refresh that lost the playing stream kind

- **WHEN** a stream-token refresh returns no stream of the kind that is playing (for example HLS
  was disabled during playback)
- **THEN** the player never loads a stream of another kind into the running engine and treats the
  refresh as failed (reveal-terminal)

#### Scenario: Native HLS engine

- **WHEN** this browser plays HLS natively (no hls.js), the player cannot renegotiate and the
  viewer picks a rendition
- **THEN** the player switches to the HLS stream at the current position without a pin, because a
  native element exposes no level control, and the quality menu shows Auto

#### Scenario: Quality pick while native HLS is active

- **WHEN** the native HLS engine is already playing (no hls.js), the player cannot renegotiate and
  the viewer picks a rendition
- **THEN** the player pins nothing and the quality menu keeps showing Auto

#### Scenario: Rendition without an id

- **WHEN** the selected rendition matches no level
- **THEN** the player pins nothing

## ADDED Requirements

### Requirement: The quality pick is remembered per browser

The web player SHALL remember the viewer's last quality pick in this browser as a rendition id and
its bandwidth, and SHALL send that bandwidth as `capabilityProfile.maxStreamingBitrate` on every
playback-info request until the viewer chooses Auto. A blocked or unreadable store, or a stored
value that is not a positive bandwidth, SHALL mean Auto.

#### Scenario: Pick survives a new playback

- **WHEN** the viewer picks the 720p rendition and later opens another item in the same browser
- **THEN** that item's playback-info request carries the 720p rendition's bandwidth as
  `maxStreamingBitrate`

#### Scenario: Storage unavailable

- **WHEN** local storage throws or holds a malformed value
- **THEN** playback-info requests carry no `maxStreamingBitrate`

### Requirement: The quality menu shows and resets the remembered pick

The quality menu SHALL mark the remembered rendition when the stream lists it, SHALL offer Auto
beside Original on direct play while a pick is remembered, and choosing Auto, or Original on direct
play, SHALL forget the pick.

#### Scenario: Remembered step is marked

- **WHEN** a 720p pick is remembered and the stream lists a 720p rendition
- **THEN** the quality menu marks 720p as selected

#### Scenario: Reset to Auto

- **WHEN** a pick is remembered and the viewer chooses Auto
- **THEN** the pick is forgotten and the player re-requests playback info at the current position
  with no `maxStreamingBitrate`
