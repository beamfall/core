## ADDED Requirements

### Requirement: Lyrics read for audio Media

The server SHALL answer `mediaLyrics(mediaID)` for a session with `synced`, `source` and `lines`;
each line carries its `text` and, when synced, its `startMs`. LRC text SHALL be parsed into lines
ordered by start time. A Media the session cannot see SHALL answer `unknown media`.

#### Scenario: Unknown media

- **WHEN** the session cannot see the Media
- **THEN** `mediaLyrics` answers `unknown media`

### Requirement: Local lyric source order

For an audio Media the server SHALL use the first source that yields lyrics: a same-name `.lrc`
beside the primary file, an embedded ID3v2 SYLT frame, the plugin-hydrated `lyrics` field, an
embedded ID3v2 USLT frame, then a same-name `.txt` beside the primary file.

#### Scenario: Synced sidecar

- **WHEN** an audio item `Song.flac` has a `Song.lrc` beside it with timestamped lines
- **THEN** `mediaLyrics` answers `synced: true`, `source: "sidecar-lrc"` and each line with its
  start time in milliseconds, ordered by time

#### Scenario: Plain text sidecar

- **WHEN** an audio item has no `.lrc`, no lyric tags and a same-name `.txt` beside it
- **THEN** `mediaLyrics` answers `synced: false` and the file's lines without start times

#### Scenario: Embedded SYLT

- **WHEN** an MP3 has no `.lrc` beside it and carries an ID3v2 SYLT frame with millisecond stamps
- **THEN** `mediaLyrics` answers `synced: true`, `source: "embedded"` and the frame's lines

#### Scenario: No lyrics

- **WHEN** an audio item has no sidecar, no lyric tags and no `lyrics` field
- **THEN** `mediaLyrics` answers `synced: false` and no lines

#### Scenario: Non-audio item

- **WHEN** the item is not audio
- **THEN** `mediaLyrics` answers no lines and reads no sidecar
