## ADDED Requirements

### Requirement: The scan records audio sample rate and bit depth

For every audio stream, the scan SHALL record the sample rate in Hz and the bit depth in bits per
sample as ffprobe reports them. A value ffprobe does not report, or reports out of bounds, SHALL be
recorded as zero, which means not recorded.

#### Scenario: Hi-res FLAC

- **WHEN** a 96 kHz 24-bit FLAC file is scanned
- **THEN** its audio stream records sample rate 96000 and bit depth 24

#### Scenario: Lossy stream without a bit depth

- **WHEN** a 48 kHz AAC stream is scanned
- **THEN** its audio stream records sample rate 48000 and bit depth 0

### Requirement: The decision honours audio sample-rate and bit-depth ceilings

A capability profile's `audioCodecs[]` entry MAY declare `maxSampleRate` and `maxBitDepth`; 0 or
omitted SHALL mean no ceiling. The decision SHALL NOT direct-play or pass through a source audio
stream whose recorded sample rate or bit depth exceeds the matching codec entry's ceiling, and
SHALL transcode it instead. Passthrough SHALL be decided on the selected audio track alone. A
ceiling SHALL NOT refuse a stream whose value was not recorded.

#### Scenario: Sample rate above the ceiling

- **WHEN** a client declaring FLAC up to 96000 Hz requests a 192 kHz 24-bit FLAC item
- **THEN** the decision is `transcode`

#### Scenario: Within the ceilings

- **WHEN** the same client requests a 96 kHz 24-bit FLAC item
- **THEN** the decision is `direct-play`

#### Scenario: Unrecorded values

- **WHEN** the same client requests a FLAC item whose sample rate and bit depth were not recorded
- **THEN** the decision is `direct-play`

#### Scenario: Another track fits the ceiling

- **WHEN** a client passing TrueHD through up to 48000 Hz selects a 192 kHz TrueHD track of an item
  whose other TrueHD track is 48 kHz
- **THEN** the selected track is re-encoded, not copied, and `audioQuality.delivery` is `transcode`

### Requirement: Playback info states the source audio quality

`playbackInfo` SHALL carry `audioQuality {codec, sampleRate, bitDepth, lossless, delivery}` for
the selected audio track of `streams[0]`, and `next.audioQuality` for the declared next item.
`sampleRate` and `bitDepth` SHALL be omitted when not recorded, and `lossless` SHALL be true for a
lossless source codec. `audioQuality` SHALL be omitted when the item is unavailable or has no
audio stream.

#### Scenario: Unrecorded values

- **WHEN** a FLAC item whose sample rate and bit depth were not recorded direct-plays
- **THEN** `audioQuality` is codec `flac`, lossless, with no `sampleRate` or `bitDepth`

### Requirement: Playback info states whether delivery is lossless direct play or a transcode

`audioQuality.delivery` SHALL be `direct` when `streams[0]` carries the source audio bit-for-bit:
direct play, or a live transcode copying a track the client declared it passes through within its
ceilings. It SHALL be `transcode` otherwise.

#### Scenario: Lossless direct play

- **WHEN** a 96 kHz 24-bit FLAC item direct-plays
- **THEN** `audioQuality` is codec `flac`, sample rate 96000, bit depth 24, lossless, delivery
  `direct`

#### Scenario: Transcoded hi-res source

- **WHEN** a 192 kHz 24-bit FLAC item is transcoded because it exceeds the client's ceiling
- **THEN** `audioQuality` is codec `flac`, sample rate 192000, bit depth 24, lossless, delivery
  `transcode`
