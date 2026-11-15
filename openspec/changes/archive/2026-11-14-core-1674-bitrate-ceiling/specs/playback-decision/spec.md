## ADDED Requirements

### Requirement: A viewer streaming ceiling bounds the delivered bitrate

When a capability profile carries a positive `maxStreamingBitrate`, playback negotiation SHALL NOT
hand over a video source by direct play, direct stream or audio copy unless its bitrate is known
and within the ceiling, and SHALL NOT advertise a rendition above the ceiling, except that a
ceiling below the lowest rung SHALL leave the lowest rung. Audio-only items are not bound.

#### Scenario: Source above the ceiling

- **WHEN** a 6 Mbit/s HEVC source the profile can direct play is negotiated with a 5 Mbit/s ceiling
- **THEN** the decision is `transcode` with reason `bitrate-ceiling` and no rendition exceeds
  5 Mbit/s

#### Scenario: Source below the ceiling

- **WHEN** the same source is negotiated with a 10 Mbit/s ceiling and its bitrate is known
- **THEN** the source is direct played

#### Scenario: Unknown source bitrate

- **WHEN** the source bitrate is unknown and the profile carries a ceiling
- **THEN** the decision is `transcode` with reason `bitrate-ceiling`

#### Scenario: Ceiling below the ladder

- **WHEN** the ceiling is lower than the lowest transcode rung
- **THEN** only the lowest rung is advertised

#### Scenario: HEVC rungs that fit stay

- **WHEN** a 2160p source is transcoded for an HEVC-capable client under a 40 Mbit/s ceiling
- **THEN** the 2160p HEVC rung is still advertised

#### Scenario: Transcoded audio fits the rung

- **WHEN** a source the profile could pass lossless audio through for is transcoded under a ceiling
- **THEN** the audio is re-encoded to stereo AAC, within the audio share each rung reserves

#### Scenario: Audio-only items keep their audio

- **WHEN** an audio-only item is transcoded for another reason while a ceiling is set
- **THEN** its audio codec and channels are what they would be without the ceiling

### Requirement: The decision names the viewer ceiling only when it alone applies

When the viewer's streaming ceiling is the only reason a video source was not handed over,
the playback decision reason SHALL be `bitrate-ceiling`. Otherwise the reason SHALL be what it
would be without the ceiling.

#### Scenario: Ceiling alone

- **WHEN** a source the profile can direct play exceeds the ceiling
- **THEN** the reason is `bitrate-ceiling`

#### Scenario: Another refusal

- **WHEN** the profile cannot decode the source codec and carries a ceiling
- **THEN** the reason is `requires-transcode`

#### Scenario: Cached rendition taken anyway

- **WHEN** a source the profile cannot direct play has a ready cached rendition and the profile
  carries a ceiling
- **THEN** the reason is `cached-rendition`

#### Scenario: Dashboard reason

- **WHEN** the profile cannot decode the source codec and carries a ceiling
- **THEN** the live-session dashboard names the codec, not the bitrate, as the reason

#### Scenario: Dolby Vision base layer refused by the ceiling

- **WHEN** a Dolby Vision profile 7 source would direct play its HDR10 base layer without the
  ceiling and the ceiling refuses it
- **THEN** the reason is `bitrate-ceiling` and the dashboard names the bitrate, not the HDR format
