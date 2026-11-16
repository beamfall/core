## ADDED Requirements

### Requirement: The next track is decoded before the current one ends

While a music track without a picture plays, the web player SHALL fetch and decode the next
track's direct-play stream no later than 30 s before the current track ends, and abandon it when
the item or the next track changes. It SHALL do so only when that next track is the one the player
advances to, its stream is served from the player's origin, the browser offers Web Audio, no audio
delay is set and no Watch Party leads the player.

#### Scenario: Album track near its end

- **WHEN** an album track plays with 30 s or less left and playback-info declares the next album
  track with a direct-play stream
- **THEN** the player has fetched and is decoding that stream before the current track ends

#### Scenario: Current track served over HLS

- **WHEN** the current track plays over HLS and the next declares a direct-play stream
- **THEN** the next track is decoded and joined as for a direct-play current track

#### Scenario: Next track served over HLS

- **WHEN** the next track's only stream is an HLS transcode
- **THEN** nothing is decoded and the track changes through the media element as before

### Requirement: The media element is never routed through Web Audio

The decoded tracks SHALL play on an audio output of their own. The join SHALL NOT create a media
element source for the player's media element, in any browser, so a later item the element plays
over the browser's own HLS engine keeps its sound. From the end of its track the element SHALL play
muted while it shadows a decoded track, and the player's mute control SHALL show and set the
listener's own mute, which applies to the decoded track.

#### Scenario: Native HLS item after a join

- **WHEN** a decoded track has joined and a later item plays over the browser's own HLS engine
- **THEN** that item is heard through the media element

#### Scenario: Listener mutes during a decoded track

- **WHEN** the listener mutes while a decoded track plays
- **THEN** the decoded track goes silent, the control shows muted, and unmuting brings it back

### Requirement: Decoded audio is bounded

The web player SHALL NOT decode a next track whose metadata declares more than 12 minutes or no
finite duration, nor keep one that decodes longer than that. It SHALL release each decoded track as
soon as it finishes or is stopped, and hold at most two decoded tracks at once.

#### Scenario: Long next track

- **WHEN** the next album track declares a duration of 13 minutes
- **THEN** nothing is fetched or decoded and the track changes through the media element as before

#### Scenario: Decoded track finishes

- **WHEN** a decoded track plays to its end
- **THEN** its decoded audio is released

### Requirement: Track changes join without a gap

The decoded next track SHALL start on the Web Audio clock where the current track's audio stops.
The first join after the media element played the current track SHALL leave no silence, and no
overlap of the two tracks, longer than 20 ms beyond the source's own; each later join between two
decoded tracks SHALL leave neither longer than 2 ms. The muted media element SHALL stay within
0.25 s of what is heard, so the transport, progress reports and the advance keep following it.

#### Scenario: Element to decoded track

- **WHEN** the first album track ends with the second decoded
- **THEN** the second track is heard within 20 ms of the first track's last sample

#### Scenario: Decoded track to decoded track

- **WHEN** the second album track ends with the third decoded
- **THEN** the third track is heard within 2 ms of the second track's last sample

### Requirement: The server's gapless trim is applied once

When a stream declares `gapless {sampleRate, encoderDelaySamples, encoderPaddingSamples}`, the
player SHALL skip the declared delay at the start and the declared padding at the end of the decoded
track, and start the decoded track where the media element's declared padding begins, when it
joins. A declared span
whose decoded samples are not near silence (peak at or above 0.01 full scale) SHALL NOT be skipped,
since the browser's decoder has already trimmed it.

#### Scenario: Declared delay and padding

- **WHEN** a track declares 2112 delay and 1500 padding samples of silence at 44.1 kHz
- **THEN** neither silence is heard at the joins

#### Scenario: Already trimmed by the decoder

- **WHEN** the declared delay span of the decoded track holds sound
- **THEN** the track starts at its first decoded sample

### Requirement: Any other playback change hands sound back to the element

A pause, a seek, a playback rate change, an advance to any item other than the decoded track,
unmounting the player, turning on an audio delay, or anything other than the join unmuting the
element SHALL stop the decoded audio and restore the media element's sound, with the listener's own
mute, at the element's own position. The element's volume and the listener's mute SHALL apply to the
decoded audio while it plays.

#### Scenario: Seek during a decoded track

- **WHEN** the listener seeks while a decoded track plays
- **THEN** the decoded audio stops and the element plays from the seek position at its own volume
