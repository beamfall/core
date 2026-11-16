# playback-decision Specification

## Purpose
Defines playback decisions for undeclared containers and audio items: remux rather than transcode when streams are playable, and judge audio by its codec.

## Requirements

### Requirement: An undeclared container with playable streams is remuxed, not transcoded

When the source container is not in the client's declared containers but the video stream, the
audio streams and the selected subtitle format are all playable by the client, playback
negotiation SHALL decide `direct-stream` with reason `container-remux` and a single video-copy
rendition. It SHALL NOT take this path for a source with no recorded video stream, or whose video
carries HDR10+ dynamic metadata or Dolby Vision metadata; those keep the transcode (or refusal)
behaviour.

#### Scenario: mkv with H.264 and AAC for a browser profile

- **WHEN** a `matroska` file with H.264 High video and stereo AAC audio is negotiated for a profile
  that declares `mp4` and `webm` containers, H.264 and AAC
- **THEN** the decision is `direct-stream` with reason `container-remux`

#### Scenario: Video the client cannot decode

- **WHEN** the same container carries HEVC video and the profile declares only H.264
- **THEN** the decision is `transcode`

#### Scenario: Source with no recorded streams

- **WHEN** an undeclared-container file has no stream inventory
- **THEN** the decision is `transcode`

#### Scenario: HDR10+ source

- **WHEN** an undeclared-container file carries HDR10+ metadata
- **THEN** the decision is `transcode`

### Requirement: An audio item is judged by its audio codec

For an audio-class item, playback negotiation SHALL NOT require the item's codec to appear in the
profile's `videoCodecs`; the audio streams SHALL be judged against the profile's `audioCodecs`.

#### Scenario: mp3 against a browser profile

- **WHEN** an mp3 audio item is negotiated for a profile that declares the `mp3` container and the
  `mp3` audio codec alongside `videoCodecs`
- **THEN** the decision is `direct-play`

#### Scenario: Undeclared audio codec

- **WHEN** the profile omits the `mp3` audio codec
- **THEN** the decision is not `direct-play`

### Requirement: The web client declares the audio it can play from a file

The web capability probe SHALL declare an audio file container when the browser can play it, using
the container names the server's probe records (`mp3`, `flac`, `ogg`, `m4a`, `wav`), and SHALL
declare the `mp3` and `vorbis` audio codecs when the browser can decode them from a file.

#### Scenario: Browser plays mp3

- **WHEN** the browser reports it can play `audio/mpeg`
- **THEN** the probed profile lists the `mp3` container and the `mp3` audio codec

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

### Requirement: A viewer quality step is honoured below every cap

A viewer quality step is a `maxStreamingBitrate` equal to an advertised rendition's `bandwidth`.
Negotiation under such a ceiling SHALL advertise that rendition as the highest one, SHALL NOT
advertise any rendition above the lower of that ceiling and any admin remote cap that applies, and
a request without a ceiling SHALL negotiate exactly as it did before quality steps existed.

#### Scenario: Ladder step tops out at that step

- **WHEN** a source that transcodes to 1080p, 720p and 480p rungs is requested with the 720p
  rung's bandwidth as its ceiling
- **THEN** the highest advertised rendition is the 720p rung and none exceeds its bandwidth

#### Scenario: Admin cap tighter than the step

- **WHEN** a remote request picks the 1080p step while the profile's remote cap is 3 Mbit/s
- **THEN** no advertised rendition exceeds 3 Mbit/s

#### Scenario: Auto is today's negotiation

- **WHEN** the same request carries no ceiling
- **THEN** the decision, reason and renditions match a request made without the field
