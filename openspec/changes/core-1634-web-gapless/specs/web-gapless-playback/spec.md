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

### Requirement: A decodable track that starts from silence plays on Web Audio

When playback of a music track without a picture starts from silence (a play or a seek) and that
track meets the conditions for decoding a next track, the player SHALL hold the element muted, decode
the track and start it on Web Audio at the element's position. If it cannot start within 2 s, or the
track cannot be decoded, the element SHALL play it audibly from that position instead.

#### Scenario: Album playback starts

- **WHEN** the listener plays an album's first track, a direct-play stream of a few minutes
- **THEN** the track is heard from Web Audio, the element muted beside it

#### Scenario: Decode too slow

- **WHEN** the track has not started on Web Audio 2 s after the listener pressed play
- **THEN** the element plays it with sound from where it started

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

The web player SHALL NOT decode a track whose metadata declares more than 12 minutes or no finite
duration, nor keep one that decodes longer than that. It SHALL release each decoded track as soon as
it finishes or the item moves past it, and hold at most two decoded tracks at once: the one heard
and the next.

#### Scenario: Long next track

- **WHEN** the next album track declares a duration of 13 minutes
- **THEN** nothing is fetched or decoded and the track changes through the media element as before

#### Scenario: Decoded track finishes

- **WHEN** a decoded track plays to its end
- **THEN** its decoded audio is released

### Requirement: Track changes join without a gap

The decoded next track SHALL start on the Web Audio clock where the current track's audio stops.
A join between two decoded tracks SHALL leave no silence, and no overlap of the two tracks, longer
than 2 ms beyond the source's own. A join after the media element played the current track audibly
SHALL leave neither longer than 20 ms.

#### Scenario: First track to second

- **WHEN** the first album track, started on Web Audio, ends with the second decoded
- **THEN** the second track is heard within 2 ms of the first track's last sample

#### Scenario: Element to decoded track

- **WHEN** the element played the current track audibly and it ends with the next decoded
- **THEN** the next track is heard within 20 ms of the current track's last sample

#### Scenario: Decoded track to decoded track

- **WHEN** the second album track ends with the third decoded
- **THEN** the third track is heard within 2 ms of the second track's last sample

### Requirement: The muted element follows what is heard

While a decoded track is heard, the muted media element SHALL be moved back within 0.25 s of what is
heard, at most once a second, so the transport, progress reports and the advance keep following it.

#### Scenario: Element drifts from the decoded track

- **WHEN** the muted element is more than 0.25 s from the decoded track's position
- **THEN** it is moved to that position, and not again within the next second

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

### Requirement: Pause and seek keep a decoded track on Web Audio

A pause SHALL stop the decoded audio and keep the element muted. Playing again, or a seek, SHALL
start the decoded track again at the element's position, so a track heard from Web Audio is never
handed to the element mid-play.

#### Scenario: Seek during a decoded track

- **WHEN** the listener seeks while a decoded track plays
- **THEN** the decoded track is heard from the seek position and the element stays muted

#### Scenario: Pause and resume

- **WHEN** the listener pauses a decoded track and plays it again
- **THEN** it is heard again from where it paused

### Requirement: Any other playback change hands sound back to the element

A playback rate change, an advance to any item other than the decoded track, unmounting the player,
turning on an audio delay, or anything other than the player unmuting the element SHALL stop the
decoded audio and restore the media element's sound, with the listener's own mute, at the element's
own position. The element's volume and the listener's mute SHALL apply to the decoded audio while it
plays.

#### Scenario: Playback rate change during a decoded track

- **WHEN** the listener changes the playback rate while a decoded track plays
- **THEN** the decoded audio stops and the element plays from its position at its own volume
