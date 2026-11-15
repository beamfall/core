## ADDED Requirements

### Requirement: Pitch shift and tempo are validated playback request options

playbackInfo SHALL accept an optional `adjustment` with `pitchShiftSemitones`, a whole number from
-6 to +6, and `tempo`, a factor from 0.75 to 1.25, and SHALL reject any other value with a 400.
An absent adjustment, a zero pitch shift and a tempo of 1 SHALL leave the decision unchanged.

#### Scenario: Out of range

- **WHEN** a client asks for `pitchShiftSemitones` 7 or `tempo` 1.5
- **THEN** playbackInfo answers 400 and mints no stream

#### Scenario: Defaults change nothing

- **WHEN** a client sends `pitchShiftSemitones` 0 and `tempo` 1
- **THEN** the decision is the same as with no adjustment, and the response has no `adjustment`

### Requirement: A non-default adjustment forces a transcode of an audio Media

For an audio Media, a non-default adjustment SHALL be applied by a live transcode bound to the
stream grant: the live composed picture when the Media has one and the client takes H.264 video,
otherwise an audio-only HLS stream. The pitch SHALL change without changing the tempo, and the
tempo without changing the pitch. The response SHALL echo the applied `adjustment`.

#### Scenario: Audio without a picture

- **WHEN** a client that could direct-play a song asks for `pitchShiftSemitones` -2
- **THEN** the decision is `transcode` with an audio-only HLS stream, the grant carries the pitch
  shift, and the response echoes `adjustment`

#### Scenario: Song with a composed picture

- **WHEN** an H.264 client asks for `tempo` 0.9 on a song whose composed rendition is Ready
- **THEN** the decision is a live composed `transcode`, not the precomputed rendition

#### Scenario: Not applicable

- **WHEN** the Media is a video, or the transcode policy refuses a stream
- **THEN** the decision is the ordinary one and the response has no `adjustment`

### Requirement: Adjusted streams keep song position and picture in step

An adjusted HLS stream SHALL last the song duration divided by the tempo, and its output segment N
SHALL start at song position N x segment length x tempo, so that a seek or restart resumes the same
song position. A composed picture SHALL be retimed with the audio.

#### Scenario: Restart mid-song

- **WHEN** the encoder of a stream at tempo 0.8 restarts at output segment 10 of 4 seconds
- **THEN** the audio input seeks to song position 32 seconds and the segment timestamps start at
  40 seconds

#### Scenario: Timed text under tempo

- **WHEN** a timed-text picture is composed at tempo 1.25
- **THEN** the text is rendered at song time and the frames are retimed to stream time

### Requirement: The adjustment ranges are advertised

GraphQL `serverInfo` SHALL report `playbackAdjustments` with `pitchShiftSemitonesMin`,
`pitchShiftSemitonesMax`, `tempoMin` and `tempoMax`, so a client builds its controls from the
server's ranges.

#### Scenario: Client reads the ranges

- **WHEN** a client queries `serverInfo { playbackAdjustments { tempoMin tempoMax } }`
- **THEN** it receives 0.75 and 1.25
