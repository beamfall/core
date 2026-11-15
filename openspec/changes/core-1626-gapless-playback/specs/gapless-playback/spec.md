## ADDED Requirements

### Requirement: The scan records encoder delay and padding

For each audio stream of an audio-only source, the scan SHALL record the sample rate, the encoder
delay and the end padding in samples, and the decoded content length, as FFmpeg reports them for
MP3, AAC/M4A and Opus. An iTunes `iTunSMPB` tag SHALL override the delay and padding of the first
audio stream. A stream that declares neither SHALL record zero, which means not declared.

#### Scenario: AAC M4A

- **WHEN** a 2 s 44.1 kHz AAC M4A file is scanned
- **THEN** its audio stream records sample rate 44100, delay 1024, padding 888 and 88200 decoded
  samples

#### Scenario: MP3 with a LAME header

- **WHEN** an MP3 file whose LAME header declares a delay and a padding is scanned
- **THEN** its audio stream records the header's delay and padding and the exact content length

### Requirement: Playback info declares the gapless trim

`playbackInfo` SHALL carry `streams[].gapless {sampleRate, encoderDelaySamples,
encoderPaddingSamples}` on a direct-play choice when the source declares a delay or a padding, and
on a live HLS transcode of a source without a picture that encodes to AAC from the beginning of the
item at a sample rate the AAC encoder accepts. A live transcode's container SHALL NOT trim the
declared samples itself, so a client trims them exactly once.

#### Scenario: Direct play of a declaring source

- **WHEN** a direct-playable MP3 that declares delay 1105 and padding 551 at 44.1 kHz is negotiated
- **THEN** the direct-play choice carries `gapless {sampleRate: 44100, encoderDelaySamples: 1105,
  encoderPaddingSamples: 551}`

#### Scenario: Two transcoded pieces of one tone join without a gap

- **WHEN** one continuous tone is cut into two AAC M4A pieces, each is transcoded through the live
  HLS session from its beginning, decoded, and trimmed by its reported delay and padding
- **THEN** each trimmed piece has exactly its source's content length and the joined audio has no
  silent run at the join longer than 64 samples

### Requirement: Streams without exact trim values omit it

Resumed, audio-copy, precomputed, pitch/tempo-adjusted and composed-picture streams SHALL omit
`streams[].gapless`, because the server cannot state their exact delay and padding.

#### Scenario: Resumed transcode

- **WHEN** the live transcode starts at a resume position
- **THEN** the transcode choice carries no `gapless` field
