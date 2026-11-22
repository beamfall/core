# media-runtime-probe Specification

## Purpose
Give every playable file a correct runtime, even when its container header carries none, so the player's duration, scrubber and saved progress follow the source rather than the transcode.

## Requirements

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

### Requirement: Playback fills a missing stored runtime

A successful playback probe of a Media row's primary single-Part Version
SHALL fill its zero stored runtime after verifying its sampled content
fingerprint, so later catalog reads can report it.
The write SHALL preserve any non-zero stored runtime and SHALL require
the protected catalog to be unlocked for an Encrypted Library. Playback
SHALL continue with the probed runtime if persistence fails.

#### Scenario: A catalog read follows a successful playback probe

- **GIVEN** the Media row's primary Version has one Part with a matching sampled fingerprint and a stored runtime of zero
- **WHEN** playback probes that file at 7152 seconds
- **THEN** a subsequent GraphQL Media read returns `durationSeconds` 7152

#### Scenario: A scan records a runtime during playback probing

- **WHEN** a scan stores a positive runtime before the playback probe writes
- **THEN** the store keeps the scan's runtime

#### Scenario: A runtime write targets a locked Encrypted Library

- **WHEN** a playback runtime write targets a locked Encrypted Library
- **THEN** no runtime is written

#### Scenario: Playback probes a legacy file or an individual Part of a multi-Part Version

- **WHEN** playback probes a file with a legacy full-file fingerprint or a Part of a multi-Part Version
- **THEN** playback can use the probed result for that file without requesting a catalog write
- **AND** playback skips full-file fingerprint verification for the legacy file
