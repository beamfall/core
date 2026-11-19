# playback-source-end Specification

## Purpose
Defines how Core treats a source that ended before its expected duration. An observed premature end is remembered per source generation across cache eviction and restart, so the server never advertises playback or a resume it already knows will fail; a changed source becomes eligible again.

## Requirements

### Requirement: Observed incomplete sources remain unavailable across restart
Core SHALL retain observed premature EOF by stable source identity and generation beyond cache eviction and process restart. An unchanged incomplete source SHALL NOT advertise playback or a resume, and its Media SHALL be excluded from Continue Watching and the in-progress projection before pagination. Original playback progress SHALL remain intact. A changed source generation SHALL be eligible again.

#### Scenario: Restart after observed premature EOF
- **WHEN** an encoder observes a premature source end and the server restarts
- **THEN** playbackInfo does not offer a resume for the unchanged source
- **AND** Continue Watching and the in-progress projection omit its Media

#### Scenario: Source repaired
- **WHEN** a source is replaced or grows after a premature EOF observation
- **THEN** the old observation does not block the new generation
- **AND** the saved progress is available again

### Requirement: Encrypted observations stay in their Library
Encrypted-source observations SHALL reside in the owning encrypted Library catalog and use the content-addressed object identity.

#### Scenario: Encrypted source
- **WHEN** encrypted playback observes premature EOF and the session is replaced
- **THEN** the source remains unavailable by its stable object identity
- **AND** its observation does not leak into the shared catalog
