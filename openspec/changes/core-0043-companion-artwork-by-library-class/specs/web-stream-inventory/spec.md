## MODIFIED Requirements

### Requirement: Direct-play decode failure falls over to HLS

On a `MediaError` code 3 or 4 during direct play, the player SHALL switch once to the HLS stream at
the current position when one is offered and playable, and otherwise SHALL show the
unsupported-codec message. The unsupported-codec message SHALL say that this browser cannot decode
the format and SHALL NOT ask anyone to turn on transcoding or conversion for the Profile; a Profile
setting is named only by the `transcode-disallowed` refusal, which the server returns when the
Profile's `transcodeAllowed` is false.

#### Scenario: Decode failure before playback began

- **WHEN** the decode error fires before playback started, or while the viewer was playing
- **THEN** the HLS stream is attached and playback starts or resumes; a viewer's pause is kept

#### Scenario: Decode failure with an HLS pin

- **WHEN** the native player reports `MediaError` 3 and an HLS stream is available
- **THEN** the player attaches the HLS stream at the position it had

#### Scenario: Decode failure before the resume seek applied

- **WHEN** the decode error fires before the requested start position was applied to the native player
- **THEN** the HLS stream starts at the requested start position, not at zero

#### Scenario: Details show the active route after the switch

- **WHEN** direct play has switched to HLS
- **THEN** the playback details dialog shows the server transcode route, not direct play

#### Scenario: Decode failure with no HLS stream

- **WHEN** the native player reports `MediaError` 4 during direct play and no HLS stream is offered
- **THEN** the player shows the unsupported-codec message, which names no Profile setting
