## ADDED Requirements

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
