## ADDED Requirements

### Requirement: Track listing without a playback session

The graph SHALL offer a session-role `mediaTracks(mediaID)` read listing every Version of a Media
item in playback order, each with the id `playbackInfo` reports for it, its stored label, and its
video codec and HDR format when known. The read SHALL NOT negotiate a stream, issue a stream token
or start a playback session, and SHALL answer `unknown media` for a Media the session cannot see.

#### Scenario: Two Versions

- **WHEN** a Media item has two Versions
- **THEN** `mediaTracks` lists both, in playback order, with the ids `playbackInfo` reports

#### Scenario: Media outside the session

- **WHEN** a session asks for a Media item in a Library it cannot see
- **THEN** the read answers `unknown media`

### Requirement: Listed tracks of a Version

Each Version in `mediaTracks` SHALL list the audio and caption tracks of its primary Part. An audio
track SHALL carry its stream index, codec, language, channels and the default, commentary and
visually impaired roles. A caption track SHALL carry the id `playbackInfo` gives the same track,
the stream index of a muxed track, its format, language and the forced, SDH, visually impaired and
commentary roles.

#### Scenario: Audio and a muxed subtitle

- **WHEN** a Version's primary Part has two audio streams and a muxed subtitle
- **THEN** it lists both audio streams with the file's default marked, and the subtitle with its
  stream index and its `playbackInfo` id
