## ADDED Requirements

### Requirement: Language tags match canonically

Track language matching SHALL compare canonical language tags, so a three-letter ISO 639-2 code
matches the two-letter BCP 47 tag for the same language, case-insensitively, and a region-qualified
tag matches its base language.

#### Scenario: Probed three-letter code

- **WHEN** a stream is tagged `eng` and the Profile prefers `en`
- **THEN** the stream is in a preferred language

### Requirement: The preferred audio language is delivered on every route

When a request names no audio stream, the server SHALL deliver the stream in the most preferred
Profile audio language the file has, preferring a main track over commentary or description and
the file default within a language. When that stream is not the file default, the decision SHALL
NOT be direct play.

#### Scenario: Two-language file playable directly

- **WHEN** a direct-playable MP4 has an English default and a French stream, and the Profile
  prefers French
- **THEN** the decision is not direct play, the stream token binds the French stream and the
  response marks it selected

#### Scenario: Default already preferred

- **WHEN** the Profile prefers the file default's language
- **THEN** the file plays directly

### Requirement: The web player starts on the server's subtitle pick

The web player SHALL apply `selectedSubtitleTrack` once per Media, before any viewer pick, on every
route: it SHALL show a matching text track, and for a picture-based track it SHALL request playback
info again with that stream as `selectedSubtitleIndex`.

#### Scenario: Forced subtitles on direct play

- **WHEN** a direct-play response names a forced French muxed track as the start pick
- **THEN** that text track is showing once the stream attaches

#### Scenario: Picture-based pick

- **WHEN** the start pick is a picture-based stream
- **THEN** the player requests playback info once more with that stream index to burn in
