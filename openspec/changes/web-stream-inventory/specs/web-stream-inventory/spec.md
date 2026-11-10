## ADDED Requirements

### Requirement: The player keeps the full stream inventory

The web player SHALL keep every stream of the playback-info response in its state and SHALL treat
the first stream as the initial route.

#### Scenario: Direct play with an HLS pin

- **WHEN** playback info lists a direct-play stream and an HLS stream
- **THEN** the player starts on the direct-play stream and exposes both streams

### Requirement: Quality selection on direct play

The quality menu SHALL list the renditions of the HLS stream when the active stream is direct play,
and selecting a rendition SHALL switch playback to HLS at the current position with that rendition
pinned.

#### Scenario: Step down from direct play

- **WHEN** the viewer picks a rendition while direct play is active
- **THEN** the player attaches the HLS stream at the current position and pins that rendition

#### Scenario: Native HLS engine

- **WHEN** this browser plays HLS natively (no hls.js) and the viewer picks a rendition
- **THEN** the player switches to the HLS stream at the current position without a pin, because a
  native element exposes no level control, and the quality menu shows Auto

#### Scenario: Rendition without an id

- **WHEN** the selected rendition matches no level
- **THEN** the player pins nothing

### Requirement: Direct-play decode failure falls over to HLS

On a `MediaError` code 3 or 4 during direct play, the player SHALL switch once to the HLS stream at
the current position when one is offered and playable, and otherwise SHALL show the
unsupported-codec message.

#### Scenario: Decode failure before playback began

- **WHEN** the decode error fires before playback started, or while the viewer was playing
- **THEN** the HLS stream is attached and playback starts or resumes; a viewer's pause is kept

#### Scenario: Decode failure with an HLS pin

- **WHEN** the native player reports `MediaError` 3 and an HLS stream is available
- **THEN** the player attaches the HLS stream at the position it had

### Requirement: Native-route relock is reveal-terminal

On a native-route `MEDIA_ERR_NETWORK`, the player SHALL probe the stream URL, and on a 401, 403 or
410 SHALL enter the reveal-terminal state and retire the identity epoch.

#### Scenario: Probe that never answers

- **WHEN** the probe takes longer than its timeout
- **THEN** it is aborted and the generic failure message is shown

#### Scenario: Relock on direct play

- **WHEN** the native player reports a network error and the probe returns 403
- **THEN** the player shows the terminal state and resets the identity epoch
