## ADDED Requirements

### Requirement: Probe parsing finds a runtime outside the container header

Probe parsing SHALL take the media runtime from `format.duration` when present, and otherwise from
the longest stream duration, then the longest stream `DURATION` tag, then a format `DURATION` tag.

#### Scenario: Runtime only in a stream tag

- **WHEN** a file's probe has no `format.duration` and a stream tag `DURATION=01:59:12.000000000`
- **THEN** the parsed runtime is 7152 seconds

### Requirement: Playback covers the whole file when the stored runtime is missing

When a video or audio Media in a library that is not encrypted has no stored runtime, the server
SHALL probe its file on demand, report the probed runtime as the playback timeline duration and
start HLS sessions with it so the playlist covers the whole file. The probe result SHALL be cached
by content fingerprint.

#### Scenario: Playback info for a Media without a stored runtime

- **WHEN** playback info is requested twice for a Media stored with a 0 runtime whose file probes
  at 7152 seconds
- **THEN** both responses report `timeline.durationSeconds` 7152 and the file is probed once

#### Scenario: A stored runtime is kept

- **WHEN** playback info is requested for a Media with a stored runtime
- **THEN** the stored runtime is reported and the file is not probed
