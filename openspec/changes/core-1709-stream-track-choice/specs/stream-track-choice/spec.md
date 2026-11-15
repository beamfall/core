## ADDED Requirements

### Requirement: Playback info lists every audio stream

The playback-info response SHALL list every audio stream of the negotiated file in `audioTracks`,
each with its absolute stream index, codec, language, channel count and the roles default,
commentary and visually impaired, and SHALL mark the stream that will be delivered as `selected`.

#### Scenario: Multi-audio file

- **WHEN** a viewer requests playback info for a file with three audio streams
- **THEN** `audioTracks` lists all three and exactly one is `selected`

### Requirement: A chosen audio stream is delivered on every route

When a request names `selectedAudioIndex` for a stream other than the file default, the server
SHALL deliver that stream, using the audio-copy or transcode route when direct play would deliver
the default, and the stream token SHALL bind the chosen stream so the segmenter maps it.

#### Scenario: Non-default audio on a direct-playable file

- **WHEN** a direct-playable file is requested with `selectedAudioIndex` naming its second audio stream
- **THEN** the decision is not direct play and the HLS segments carry the second audio stream

#### Scenario: Single audio stream

- **WHEN** a file has one audio stream
- **THEN** the stream token binds no audio stream

### Requirement: The web player offers every stream on every route

The web player SHALL build its Audio menu from `audioTracks` and its Subtitles menu from
`captionTracks` whenever the server reports them, whatever the route. Text subtitles SHALL be
shown as text tracks; choosing a bitmap stream SHALL renegotiate with `selectedSubtitleIndex` for
burn-in. When the server withholds subtitles, the Subtitles menu SHALL show a disabled row naming
the reason.

#### Scenario: HLS route with several streams

- **WHEN** a file with three audio and five subtitle streams plays on the HLS route
- **THEN** the Audio menu lists three options and the Subtitles menu lists Off and five options

#### Scenario: Switching audio

- **WHEN** the viewer chooses another audio stream
- **THEN** the player renegotiates with that `selectedAudioIndex` and marks it selected

#### Scenario: Bitmap subtitle

- **WHEN** the viewer chooses a bitmap subtitle stream
- **THEN** the player renegotiates with that `selectedSubtitleIndex` and keeps the audio choice

#### Scenario: Withheld subtitles

- **WHEN** the server reports `subtitlesUnavailableReason`
- **THEN** the Subtitles menu shows a disabled row explaining it
