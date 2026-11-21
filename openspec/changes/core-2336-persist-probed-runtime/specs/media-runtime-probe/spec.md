## ADDED Requirements

### Requirement: Playback fills a missing stored runtime

A successful playback probe of a Media row's primary single-Part Version
SHALL fill its zero stored runtime so later catalog reads can report it.
The write SHALL preserve any non-zero stored runtime and SHALL require
the protected catalog to be unlocked for an Encrypted Library. Playback
SHALL continue with the probed runtime if persistence fails.

#### Scenario: A catalog read follows a successful playback probe

- **GIVEN** the Media row's primary Version has one Part and a stored runtime of zero
- **WHEN** playback probes that file at 7152 seconds
- **THEN** a subsequent GraphQL Media read returns `durationSeconds` 7152

#### Scenario: A scan records a runtime during playback probing

- **WHEN** a scan stores a positive runtime before the playback probe writes
- **THEN** the store keeps the scan's runtime

#### Scenario: A runtime write targets a locked Encrypted Library

- **WHEN** a playback runtime write targets a locked Encrypted Library
- **THEN** no runtime is written
